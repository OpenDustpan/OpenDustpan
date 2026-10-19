// Package charttest holds the golden-file tests of the Helm chart in
// deploy/helm/opendustpan. The tests run `helm template` and compare the
// output with the files in testdata. They skip when helm is not on PATH.
package charttest
