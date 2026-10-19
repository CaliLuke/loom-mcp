package inspect

import (
	"context"
	"errors"
	"os"
	"slices"
	"sort"
)

type viewRow struct {
	manifest semanticManifest
	spec     entrySpec
	material materialEntry
}

// View binds a finite immutable scope and a previously materialized host policy.
func (w *Workspace) View(scope Scope, policy ViewPolicy) (*View, error) {
	if !validRole(policy.Role) || policy.Name == "" {
		return nil, ErrInvalidInput
	}
	refs := slices.Clone(scope.Manifests)
	sort.Slice(refs, func(i, j int) bool { return refs[i] < refs[j] })
	for i, ref := range refs {
		if i > 0 && ref == refs[i-1] {
			return nil, ErrInvalidInput
		}
		m, err := w.load(ref)
		if err != nil {
			return nil, err
		}
		found := false
		for _, p := range m.Semantic.Policies {
			if p == policy {
				found = true
			}
		}
		if !found {
			return nil, ErrNotFound
		}
	}
	return &View{workspace: w, refs: refs, policy: policy}, nil
}

// List returns only rows authorized by the host-bound view, under server caps.
func (v *View) List(ctx context.Context, q ListQuery) (Page, error) {
	if q.Offset < 0 || q.Limit < 0 {
		return Page{}, ErrInvalidInput
	}
	rows, err := v.rows(ctx)
	if err != nil {
		return Page{}, err
	}
	if q.Offset > len(rows) {
		return Page{}, ErrInvalidInput
	}
	n := q.Limit
	if n == 0 || n > v.workspace.limits.MaxPageEntries {
		n = v.workspace.limits.MaxPageEntries
	}
	end := q.Offset + min(n, len(rows)-q.Offset)
	page := Page{Entries: make([]Entry, 0, end-q.Offset), Total: len(rows), Truncated: end < len(rows)}
	if page.Truncated {
		page.NextOffset = &end
	}
	for _, row := range rows[q.Offset:end] {
		e := row.material.Entry
		if e.State != Missing && e.State != Withheld {
			_, e, err = v.content(ctx, row)
			if err != nil {
				return Page{}, err
			}
		}
		page.Entries = append(page.Entries, e)
	}
	return page, nil
}

// Read returns exact bytes from one authorized locator with bounded pagination.
func (v *View) Read(ctx context.Context, q ReadQuery) (ReadResult, error) {
	if q.Offset < 0 || q.MaxBytes < 0 {
		return ReadResult{}, ErrInvalidInput
	}
	rows, err := v.rows(ctx)
	if err != nil {
		return ReadResult{}, err
	}
	for _, row := range rows {
		if row.material.Entry.Locator != q.Locator {
			continue
		}
		data, e, err := v.content(ctx, row)
		if err != nil {
			return ReadResult{}, err
		}
		result := ReadResult{Entry: e, Offset: q.Offset}
		if e.State == Missing || e.State == Withheld {
			return result, nil
		}
		total := int64(len(data))
		if q.Offset > total {
			return ReadResult{}, ErrInvalidInput
		}
		result.Total = &total
		n := q.MaxBytes
		if n == 0 || n > v.workspace.limits.MaxReadBytes {
			n = v.workspace.limits.MaxReadBytes
		}
		end := q.Offset + min(int64(n), total-q.Offset)
		result.Data = slices.Clone(data[q.Offset:end])
		result.Truncated = end < total
		if result.Truncated {
			result.NextOffset = &end
		}
		return result, nil
	}
	return ReadResult{}, ErrNotFound
}

func missingEntry(e Entry, code string) Entry {
	e.State = Missing
	e.Code = code
	e.Incomplete = true
	e.Size = nil
	e.Digest = ""
	e.SourceDigest = ""
	e.Links = nil
	e.SourceLocator = ""
	return e
}

func (v *View) rows(ctx context.Context) ([]viewRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []viewRow
	for _, ref := range v.refs {
		m, err := v.workspace.load(ref)
		if err != nil {
			return nil, err
		}
		for i, entry := range m.Derived {
			if entry.Role == v.policy.Role && entry.Policy == v.policy.Name {
				out = append(out, viewRow{m.Semantic, m.Semantic.Entries[i], entry})
			}
		}
	}
	return out, nil
}

func (v *View) content(ctx context.Context, row viewRow) ([]byte, Entry, error) {
	e := row.material.Entry
	if e.State == Missing || e.State == Withheld {
		return nil, e, nil
	}
	data, err := v.workspace.readProvider(ctx, row.manifest, row.spec, e)
	if ctx.Err() != nil {
		return nil, Entry{}, ctx.Err()
	}
	if err != nil {
		code := "missing_content"
		if !errors.Is(err, os.ErrNotExist) && (row.spec.Provider.Kind == blobProvider || e.Kind == ComponentDiff) {
			code = "integrity_failure"
		}
		return nil, missingEntry(e, code), nil
	}
	if e.Size == nil || int64(len(data)) != *e.Size || digest(data) != e.Digest {
		return nil, missingEntry(e, "integrity_failure"), nil
	}
	if int64(len(data)) > v.workspace.limits.MaxArtifactBytes {
		return nil, Entry{}, ErrIntegrity
	}
	if e.Kind == ComponentDiff {
		_, _, blobs, err := materialize(row.manifest)
		if err != nil {
			return nil, Entry{}, ErrIntegrity
		}
		if string(blobs[e.Digest]) != string(data) {
			return nil, missingEntry(e, "integrity_failure"), nil
		}
	}
	return data, e, nil
}
