package handler

import (
	"html/template"
	"net/http"
	"slices"
	"strings"
)

// html/template escapes values: metric names come from the network and must render as text.
var indexTmpl = template.Must(template.New("index").Parse(`<!doctype html>
<html>
<head><meta charset="utf-8"><title>Metrics</title></head>
<body>
<h2>Gauges</h2>
<ul>
{{range .Gauges}}<li>{{.Name}}: {{.Value}}</li>
{{else}}<li>none</li>
{{end}}</ul>
<h2>Counters</h2>
<ul>
{{range .Counters}}<li>{{.Name}}: {{.Value}}</li>
{{else}}<li>none</li>
{{end}}</ul>
</body>
</html>
`))

type row struct {
	Name  string
	Value string
}

type indexPage struct {
	Gauges   []row
	Counters []row
}

func (h *handlers) index(w http.ResponseWriter, r *http.Request) {
	snap := h.storage.Snapshot()

	page := indexPage{
		Gauges:   make([]row, 0, len(snap.Gauges)),
		Counters: make([]row, 0, len(snap.Counters)),
	}
	for name, v := range snap.Gauges {
		page.Gauges = append(page.Gauges, row{Name: name, Value: formatGauge(v)})
	}
	for name, v := range snap.Counters {
		page.Counters = append(page.Counters, row{Name: name, Value: formatCounter(v)})
	}
	// Map order is random; sorting keeps the page stable.
	slices.SortFunc(page.Gauges, byName)
	slices.SortFunc(page.Counters, byName)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Parsed at startup, so an error here means the client is gone.
	_ = indexTmpl.Execute(w, page)
}

func byName(a, b row) int {
	return strings.Compare(a.Name, b.Name)
}
