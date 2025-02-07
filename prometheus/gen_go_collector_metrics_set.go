// Copyright 2021 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build ignore
// +build ignore

package main

import (
	"bytes"
	"fmt"
	"go/format"
	"log"
	"math"
	"os"
	"runtime"
	"runtime/metrics"
	"strings"
	"text/template"

	"github.com/adhimaswaskita/client_golang/prometheus"
	"github.com/adhimaswaskita/client_golang/prometheus/internal"

	"github.com/hashicorp/go-version"
)

func main() {
	var givenVersion string
	toolVersion := runtime.Version()
	if len(os.Args) != 2 {
		log.Printf("requires Go version (e.g. go1.17) as an argument. Since it is not specified, assuming %s.", toolVersion)
		givenVersion = toolVersion
	} else {
		givenVersion = os.Args[1]
	}
	log.Printf("given version for Go: %s", givenVersion)
	log.Printf("tool version for Go: %s", toolVersion)

	tv, err := version.NewVersion(strings.TrimPrefix(givenVersion, "go"))
	if err != nil {
		log.Fatal(err)
	}

	toolVersion = strings.Split(strings.TrimPrefix(toolVersion, "go"), " ")[0]
	gv, err := version.NewVersion(toolVersion)
	if err != nil {
		log.Fatal(err)
	}
	if !gv.Equal(tv) {
		log.Fatalf("using Go version %q but expected Go version %q", tv, gv)
	}

	v := goVersion(gv.Segments()[1])
	log.Printf("generating metrics for Go version %q", v)

	// Generate code.
	var buf bytes.Buffer
	err = testFile.Execute(&buf, struct {
		Descriptions []metrics.Description
		GoVersion    goVersion
		Cardinality  int
	}{
		Descriptions: metrics.All(),
		GoVersion:    v,
		Cardinality:  rmCardinality(),
	})
	if err != nil {
		log.Fatalf("executing template: %v", err)
	}

	// Format it.
	result, err := format.Source(buf.Bytes())
	if err != nil {
		log.Fatalf("formatting code: %v", err)
	}

	// Write it to a file.
	fname := fmt.Sprintf("go_collector_metrics_%s_test.go", v.Abbr())
	if err := os.WriteFile(fname, result, 0o644); err != nil {
		log.Fatalf("writing file: %v", err)
	}
}

type goVersion int

func (g goVersion) String() string {
	return fmt.Sprintf("go1.%d", g)
}

func (g goVersion) Abbr() string {
	return fmt.Sprintf("go1%d", g)
}

func rmCardinality() int {
	cardinality := 0

	// Collect all histogram samples so that we can get their buckets.
	// The API guarantees that the buckets are always fixed for the lifetime
	// of the process.
	var histograms []metrics.Sample
	for _, d := range metrics.All() {
		if d.Kind == metrics.KindFloat64Histogram {
			histograms = append(histograms, metrics.Sample{Name: d.Name})
		} else {
			cardinality++
		}
	}

	// Handle histograms.
	metrics.Read(histograms)
	for i := range histograms {
		name := histograms[i].Name
		buckets := internal.RuntimeMetricsBucketsForUnit(
			histograms[i].Value.Float64Histogram().Buckets,
			name[strings.IndexRune(name, ':')+1:],
		)
		cardinality += len(buckets) + 3 // Plus total count, sum, and the implicit infinity bucket.

		// runtime/metrics bucket boundaries are lower-bound-inclusive, but
		// always represents each actual *boundary* so Buckets is always
		// 1 longer than Counts, while in Prometheus the mapping is one-to-one,
		// as the bottom bucket extends to -Inf, and the top infinity bucket is
		// implicit. Therefore, we should have one fewer bucket than is listed
		// above.
		cardinality--
		if buckets[len(buckets)-1] == math.Inf(1) {
			// We already counted the infinity bucket separately.
			cardinality--
		}
		// Prometheus also doesn't have buckets for -Inf, so they need to be omitted.
		// See the following PR for more information:
		// https://github.com/adhimaswaskita/client_golang/pull/1049
		if buckets[0] == math.Inf(-1) {
			cardinality--
		}
	}

	return cardinality
}

var testFile = template.Must(template.New("testFile").Funcs(map[string]interface{}{
	"rm2prom": func(d metrics.Description) string {
		ns, ss, n, ok := internal.RuntimeMetricsToProm(&d)
		if !ok {
			return ""
		}
		return prometheus.BuildFQName(ns, ss, n)
	},
	"nextVersion": func(version goVersion) string {
		return (version + goVersion(1)).String()
	},
}).Parse(`// Code generated by gen_go_collector_metrics_set.go; DO NOT EDIT.
//go:generate go run gen_go_collector_metrics_set.go {{.GoVersion}}

//go:build {{.GoVersion}} && !{{nextVersion .GoVersion}}
// +build {{.GoVersion}},!{{nextVersion .GoVersion}}

package prometheus

var expectedRuntimeMetrics = map[string]string{
{{- range .Descriptions -}}
	{{- $trans := rm2prom . -}}
	{{- if ne $trans "" }}
	{{.Name | printf "%q"}}: {{$trans | printf "%q"}},
	{{- end -}}
{{end}}
}

const expectedRuntimeMetricsCardinality = {{.Cardinality}}
`))
