package evalcli

import (
	jsonopts "encoding/json"
	"encoding/json/v2"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
)

func list(cfg Config, f flags) error {
	store, err := openStore(cfg, f)
	if err != nil {
		return err
	}
	headers, err := store.List()
	if err != nil {
		return err
	}
	if f.limit > 0 && len(headers) > f.limit {
		headers = headers[:f.limit]
	}
	if f.json {
		return writeJSON(cfg.Stdout, headers)
	}
	var out strings.Builder
	out.WriteString("ID\tstarted\tlabel\tparent\tstatus\tstop\tplanned\ttokens\trevision\n")
	for _, h := range headers {
		revision := h.Source.Revision
		if h.Source.Modified {
			revision += "*"
		}
		fmt.Fprintf(&out, "%s\t%s\t%q\t%s\t%s\t%s\t%d\t%d\t%s\n", h.ID, h.Started.Format(time.RFC3339), h.Label, h.Parent, h.Status, h.Stop, h.Planned, h.Tokens, revision)
	}
	table := tabwriter.NewWriter(cfg.Stdout, 0, 4, 2, ' ', 0)
	if _, err := io.WriteString(table, out.String()); err != nil {
		return err
	}
	return table.Flush()
}

func writeJSON(w io.Writer, value any) error {
	data, err := json.Marshal(value, json.Deterministic(true), jsonopts.FormatDurationAsNano(true))
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}
