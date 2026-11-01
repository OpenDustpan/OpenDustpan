# SecLists: common passwords

The file `xato-net-10-million-passwords-100000.txt` is a copy of
`Passwords/Common-Credentials/xato-net-10-million-passwords-100000.txt`
from SecLists (https://github.com/danielmiessler/SecLists), at commit
`00f80ec9cc39bed63ee40cf6805a6b1b77e87644`, under the MIT license in
`LICENSE` of the same commit.

- Lines: 100000
- SHA-256 of the list: `1472aafa2561df5e3293aee252aee3ca660c12b399a283cf808bb01b39be388b`
- SHA-256 of `LICENSE`: `3dbdc93d5f8829de0941744841730a09c106d0732e5ae0e98ca1d77be7ded66c`

The package `blocklist` checks the line count and the digest of the list
when it loads. A new version of the list is a reviewed change: update the
file, the commit, the count and the digest here and in `blocklist.go`.
