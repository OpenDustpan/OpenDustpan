## 1. Fix DaysUntil and close the test gap (after #3055 is on main)

- [ ] 1.1 In `pkg/accesstoken/accesstoken.go`, make `DaysUntil` round up without the addition: `n := d / day; if d%day != 0 { n++ }`. Keep the `d <= 0` guard. Check: `rg -n 'd \+ day - 1' pkg/accesstoken/accesstoken.go` finds nothing, and `go test ./pkg/accesstoken/ -run 'TestDaysUntil|TestDaysLeft'` passes.
- [ ] 1.2 In `pkg/accesstoken/accesstoken_test.go`, add the row `far_expiry_answers_a_large_positive_count` to `TestDaysUntil`: expiry `9999-12-31T23:59:59Z`, the test checks the count is above 100000. Check: `go test ./pkg/accesstoken/ -run 'TestDaysUntil/far_expiry' -v` passes, and with the old line `int64((d + day - 1) / day)` restored it fails with a negative count.
- [ ] 1.3 In the same file, add the row `two_days_past_expiry_answers_0`: expiry `now.Add(-48 * time.Hour)`, count 0. Check: `go test ./pkg/accesstoken/ -run 'TestDaysUntil/two_days_past' -v` passes, and with the `d <= 0` branch removed it fails; paste both outputs in the PR body.
- [ ] 1.4 Run the gate and paste its output in the PR body. Check: `make check` exits 0.
