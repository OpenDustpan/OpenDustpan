// Package charttest holds the golden-file tests of the Helm chart in
// deploy/helm/opendustpan. The tests run `helm template` and compare the
// output with the files in testdata. They skip when helm is not on PATH or
// is not Helm 3. A change to the version in Chart.yaml changes the labels of
// every object, so it needs `go test ./deploy/helm/charttest -update`. One
// test needs no helm: it checks that docs/reference/helm-values.md names each
// key of values.yaml. `make chart-lint` runs the static checks of the chart.
package charttest
