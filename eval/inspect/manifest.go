package inspect

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"reflect"
	"sort"
	"strconv"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
)

type semanticManifest struct {
	CandidateDigest      string
	BaselineDigest       string
	Receipt              *eval.PreparedReceipt
	ExpectedDeliverables []eval.Deliverable
	Provisional          bool
	Version              int
	Origin               Origin
	Policies             []ViewPolicy
	Entries              []entrySpec
}
type entrySpec struct {
	Key       string
	Role      Role
	Policy    string
	Entry     Entry
	Provider  bodyProvider
	SourceKey string
	LinkKeys  []string
	Changes   []semanticChange
}
type bodyProvider struct {
	Kind      string
	Digest    string
	Size      int64
	Candidate string
	Name      string
}
type semanticChange struct {
	Name         string
	Operation    string
	BeforeDigest string
	AfterDigest  string
	BeforeKey    string
	AfterKey     string
}
type materialEntry struct {
	Key    string
	Role   Role
	Policy string
	Entry  Entry
}
type diskManifest struct {
	Semantic semanticManifest
	Derived  []materialEntry
}

const blobProvider = "blob"

func encode(v any) ([]byte, error) { return callrecord.Encode(v) }

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func validDigest(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == s
}

// materialize fixes identity before substituting any locator-bearing diff bytes.
func materialize(m semanticManifest) (ManifestRef, []materialEntry, map[string][]byte, error) {
	data, err := encode(m)
	if err != nil {
		return "", nil, nil, ErrIntegrity
	}
	ref := ManifestRef(digest(data))
	out := make([]materialEntry, 0, len(m.Entries))
	blobs := map[string][]byte{}
	locs, err := entryLocators(m, ref)
	if err != nil {
		return "", nil, nil, err
	}
	counts := map[Role]map[Kind]int{}
	for _, s := range m.Entries {
		e, body, err := deriveEntry(s, locs)
		if err != nil {
			return "", nil, nil, err
		}
		if body != nil {
			blobs[e.Digest] = body
		}

		if s.Role == Selector || s.Role == Solver {
			if counts[s.Role] == nil {
				counts[s.Role] = map[Kind]int{}
			}
			counts[s.Role][e.Kind]++
			label := "source"
			if e.Kind == Deliverable {
				label = "output"
			}
			if e.Kind == Rendered {
				label = "rendering"
			}
			e.Label = label + "-" + strconv.Itoa(counts[s.Role][e.Kind])
			e.Provenance = nil
			e.SourceDigest = ""
		}
		out = append(out, materialEntry{s.Key, s.Role, s.Policy, e})
	}
	return ref, out, blobs, nil
}

func validateManifest(m diskManifest, ref ManifestRef) error {
	if m.Semantic.Version != 1 || !validDigest(string(ref)) || !validOrigin(m.Semantic.Origin) || len(m.Semantic.Policies) == 0 {
		return ErrIntegrity
	}
	policies := map[Role]string{}
	for _, p := range m.Semantic.Policies {
		if !validRole(p.Role) || p.Name == "" || policies[p.Role] != "" {
			return ErrIntegrity
		}
		policies[p.Role] = p.Name
	}
	if !sort.SliceIsSorted(m.Semantic.Entries, func(i, j int) bool { return specLess(m.Semantic.Entries[i], m.Semantic.Entries[j]) }) {
		return ErrIntegrity
	}
	for _, s := range m.Semantic.Entries {
		if s.Key == "" || s.Policy != policies[s.Role] || !allowed(s.Entry.Kind, s.Role) || s.Entry.Locator != "" || s.Entry.SourceLocator != "" || len(s.Entry.Links) > 0 {
			return ErrIntegrity
		}
		if s.Entry.State != Available && s.Entry.State != Missing && s.Entry.State != Withheld && s.Entry.State != Provisional {
			return ErrIntegrity
		}
		if s.Entry.Kind != ComponentDiff && s.Entry.State != Missing && s.Entry.State != Withheld && (!validDigest(s.Provider.Digest) || s.Provider.Size < 0 || s.Entry.Digest != s.Provider.Digest || s.Entry.Size == nil || *s.Entry.Size != s.Provider.Size) {
			return ErrIntegrity
		}
	}
	got, derived, _, err := materialize(m.Semantic)
	if err != nil || got != ref || !reflect.DeepEqual(derived, m.Derived) {
		return ErrIntegrity
	}
	return nil
}

func specLess(a, b entrySpec) bool {
	if a.Role != b.Role {
		return a.Role < b.Role
	}
	if a.Entry.Kind != b.Entry.Kind {
		return a.Entry.Kind < b.Entry.Kind
	}
	return a.Key < b.Key
}

func strictDecode(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return ErrIntegrity
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return ErrIntegrity
	}
	return nil
}

func diffBody(s entrySpec, lookup func(string) (string, error)) ([]byte, error) {
	var err error
	changes := make([]ComponentChange, 0, len(s.Changes))
	for _, c := range s.Changes {
		v := ComponentChange{Name: c.Name, Operation: c.Operation, BeforeDigest: c.BeforeDigest, AfterDigest: c.AfterDigest}
		if c.BeforeKey != "" {
			v.BeforeLocator, err = lookup(c.BeforeKey)
			if err != nil {
				return nil, err
			}
		}
		if c.AfterKey != "" {
			v.AfterLocator, err = lookup(c.AfterKey)
			if err != nil {
				return nil, err
			}
		}
		changes = append(changes, v)
	}
	body, err := encode(changes)
	if err != nil {
		return nil, ErrIntegrity
	}
	return body, nil
}

// deriveEntry resolves protected internal keys only after semantic identity is fixed.
func deriveEntry(s entrySpec, locs map[string]string) (Entry, []byte, error) {
	var err error
	e := s.Entry
	e.Locator = locs[string(s.Role)+"/"+s.Key]
	lookup := func(key string) (string, error) {
		v, ok := locs[string(s.Role)+"/"+key]
		if !ok {
			return "", ErrIntegrity
		}
		return v, nil
	}
	if s.SourceKey != "" {
		e.SourceLocator, err = lookup(s.SourceKey)
		if err != nil {
			return Entry{}, nil, err
		}
	}
	e.Links = nil
	for _, key := range s.LinkKeys {
		loc, err := lookup(key)
		if err != nil {
			return Entry{}, nil, err
		}
		e.Links = append(e.Links, loc)
	}
	if e.Kind == ComponentDiff && e.State != Withheld {
		body, err := diffBody(s, lookup)
		if err != nil {
			return Entry{}, nil, err
		}
		d := digest(body)
		n := int64(len(body))
		e.Digest = d
		e.Size = &n
		return e, body, nil
	}
	return e, nil, nil
}

func entryLocators(m semanticManifest, ref ManifestRef) (map[string]string, error) {
	locs := map[string]string{}
	for _, s := range m.Entries {
		id := string(s.Role) + "/" + s.Key
		if _, ok := locs[id]; ok {
			return nil, ErrIntegrity
		}
		key, err := encode([]string{string(ref), string(s.Role), s.Policy, s.Key})
		if err != nil {
			return nil, ErrIntegrity
		}
		locs[id] = digest(key)
	}

	return locs, nil
}
