## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Spike

- [ ] 1.1 Write the NEW file `spike/scratch/main.go` with the scratch run of open, read, note and close. No route: the spike calls only the vendor APIs of the named sites. BLOCKED by named test sites and credentials. Check: The NEW path `spike/scratch/main.go` runs once against each named site and exits with code 0
- [ ] 1.2 Write the NEW file `spike/changeticket-spike.md` with the six answers for each system. No route: the task writes a document only. BLOCKED by the run of the scratch code. Check: The NEW path `spike/changeticket-spike.md` answers the six points for each system
- [ ] 1.3 Write the NEW file `spike/changeticket-cleanup.md` after closing each test ticket and deregistering each webhook. No route: the task calls only the vendor APIs of the named sites. Check: The NEW path `spike/changeticket-cleanup.md` lists each cleanup and each credential rotation

## 2. Definition of done

- [ ] 2.1 Usability check: an owner reads the six answers from the NEW path `spike/changeticket-spike.md` alone. A person's step. Check: `git diff --stat main` lists only files under spike/
