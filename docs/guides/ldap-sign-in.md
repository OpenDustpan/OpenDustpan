# Sign-in with an LDAP directory

This guide shows how the server checks the user name and the password of a
person at an LDAP directory, for example OpenLDAP or Active Directory. It
gives each setting of an LDAP sign-in provider, the TLS rules, the choice
of the subject, and the groups.

You set one LDAP provider in the configuration of the server: flags,
environment variables or keys of the configuration file. The API cannot add
or change an LDAP provider yet.

## Set up a provider

Give the server the URL, the provider ID, the service account, the base DN
and the user filter:

```sh
export DUSTPAN_LDAP_BIND_PASSWORD_FILE=/run/secrets/ldap-bind-password
opendustpan serve \
  --public-url https://dustpan.example.com \
  --ldap-url ldaps://ldap.example.com \
  --ldap-provider corp-ldap \
  --ldap-display-name "Corp directory" \
  --ldap-bind-dn cn=dustpan,ou=services,dc=example,dc=com \
  --ldap-base-dn ou=people,dc=example,dc=com \
  --ldap-user-filter '(&(objectClass=person)(uid={user}))'
```

The same settings in the configuration file:

```yaml
ldap:
  url: ldaps://ldap.example.com
  provider: corp-ldap
  displayName: Corp directory
  bindDN: cn=dustpan,ou=services,dc=example,dc=com
  baseDN: ou=people,dc=example,dc=com
  userFilter: (&(objectClass=person)(uid={user}))
```

The password of the service account comes only from the variable
`DUSTPAN_LDAP_BIND_PASSWORD`, or from the file that
`DUSTPAN_LDAP_BIND_PASSWORD_FILE` names. No flag and no key of the
configuration file takes it, because a process list shows each flag. The
file must be readable by its owner only. The server clears the variable
`DUSTPAN_LDAP_BIND_PASSWORD` after it reads it, so a child process does not
get the password.

The sign-in needs a public URL with HTTPS. The server checks each setting
when it starts, and does not start with a setting that is not valid. Only a
server with the `api` component reads the LDAP settings. [Configuration
reference](../reference/configuration.md) gives each flag, variable and
key.

The list of the sign-in providers, `GET /api/server/sign-in-providers`,
shows the provider with the source `static`, the mode `ldap` and the
effective value of each LDAP setting. It shows `"bindPasswordSet": true`,
never the password.

## Sign in

A person signs in with one call:

```sh
curl -X POST "$SERVER/api/dustpan/v1/sign-in/providers/corp-ldap/password" \
  -H "Content-Type: application/json" \
  -d '{"userName": "alice", "password": "<password>"}'
```

| Answer | Meaning |
| --- | --- |
| `204` | The server signed the person in. The answer sets the session cookie of the console. |
| `401` `sign-in.failed` | The server refused the sign-in. The text is "The user name or the password is wrong.", the same as for a local user. |
| `400` `request.body-invalid` | The body is not a JSON object of this route. |
| `429` | The source address has too many failed sign-ins. Try again later. |
| `503` `sign-in.unavailable` | The directory does not answer. Try again later. |

Each refusal gets the same `401` answer: a wrong password, an unknown user,
a locked name or person, a disabled person, an unknown provider, and a
request that another site can send. So the
answer does not tell that a user or a provider exists. The audit entry of
the sign-in holds the real reason.

Each `401` and each `400` counts against the source address. A `503` does
not count, because the person did not cause it.

At the first sign-in of a person, the server makes a principal with no
grant and no group. With `--ldap-no-auto-create`, the server refuses the
sign-in of a person who has no principal yet.

## How the check works

At each sign-in, the server does these steps on one connection:

1. It connects to the directory with TLS.
2. It binds with the service account.
3. It searches for the person with the user filter, below the base DN.
4. It reads the subject of the entry that it found.
5. It counts the attempt on that subject, so that each form of a user name
   counts on the same person.
6. It binds as the DN of that entry with the password that the person
   typed. The directory checks the password. The server never reads or
   compares a password hash.
7. It binds again with the service account and reads the groups, when
   groups are on.

When the search finds no entry, the server still sends one bind, with a
random DN below the base DN and a random password. So an unknown user and
a wrong password take about the same time.

The server refuses the sign-in in these cases:

- The password is empty. An empty password makes an unauthenticated bind,
  and many directories accept that bind. The server sends no bind.
- The user name is empty, longer than 256 bytes, or has a control
  character.
- The search finds no entry, or the directory refuses the password. The
  person gets the same answer for both, so the answer does not tell that
  a user exists.
- The search finds two or more entries.
- The directory answers with a referral. The server never follows a
  referral, because a referral can send the password to another server.
- The directory answers that the search is over its size limit. The server
  never uses a part of an answer.
- The directory does not answer in 10 seconds, or the read of the groups
  takes more than 30 seconds.
- The entry of the person is outside the base DN.

## Settings

| Setting | Flag | Example | Rule |
| --- | --- | --- | --- |
| URL | `--ldap-url` | `ldaps://ldap.example.com` | `ldaps://host[:port]` or `ldap://host[:port]`. No path, query or user information. |
| Provider ID | `--ldap-provider` | `corp-ldap` | 1 to 32 lower-case letters, digits and hyphens. Never change it after the first sign-in. |
| Name in the console | `--ldap-display-name` | `Corp directory` | Optional. Empty uses the provider ID. |
| StartTLS | `--ldap-start-tls` | `false` | Starts TLS on an `ldap://` URL. Not allowed with `ldaps://`. |
| CA bundle | `--ldap-ca-file` | (system roots) | A PEM file of the certificate authorities of the directory. |
| Allowed ranges | `--ldap-allowed-ranges` | `10.20.0.0/16` | Optional. See "Network". |
| Bind DN | `--ldap-bind-dn` | `cn=dustpan,ou=services,dc=example,dc=com` | The DN of the service account. |
| Bind password | (none) | (no default) | Only from `DUSTPAN_LDAP_BIND_PASSWORD` or `DUSTPAN_LDAP_BIND_PASSWORD_FILE`. |
| Base DN | `--ldap-base-dn` | `ou=people,dc=example,dc=com` | The base of the user search. |
| User filter | `--ldap-user-filter` | `(&(objectClass=person)(uid={user}))` | Holds `{user}` at least once. |
| User name attribute | `--ldap-user-name-attribute` | `uid` | Optional. Empty uses the typed user name. |
| Email attribute | `--ldap-email-attribute` | `mail` | Optional. |
| Name attribute | `--ldap-name-attribute` | `cn` | Optional. |
| Subject | `--ldap-subject` | `entryUUID` | `entryUUID`, `objectGUID`, or an attribute that you name. |
| Disabled rule | `--ldap-disabled-attribute`, `--ldap-disabled-mask`, `--ldap-disabled-values` | `userAccountControl`, mask `2` | Optional. See "Disabled persons". |
| Groups | `--ldap-groups` | `off` | `off`, `search` or `memberOf`. See "Groups". |

The server replaces each `{user}` in the user filter with the user name.
It escapes the user name first (RFC 4515), so a user name such as
`*)(uid=*` is one value, not a filter. The server also checks that the
filter with the user name has the same items as the filter with a plain
value. It refuses a user name that changes the filter. It does the same
check on each group filter.

The server builds each DN that it sends again from its parts. It escapes
each value (RFC 4514), so a value of the directory cannot add a part to a
DN.

The server never shows the bind password in a log, an error or an audit
entry.

## TLS

Use `ldaps://`, or `ldap://` with StartTLS. The server checks the
certificate of the directory against the CA bundle and the host name of
the URL. It uses TLS 1.2 or later.

The server refuses a connection with no TLS, unless the host of the URL is
a loopback IP address, for example `127.0.0.1` or `[::1]`, or the setting
in [Plain connections](#plain-connections) is on. A host name, also
`localhost`, is not enough, because its address can change.

## Network

The server checks the address of each connection when the connection opens,
after the name lookup. By default, a connection reaches public addresses
only. For a directory on a private network, set the ranges of that network
with `--ldap-allowed-ranges`, for example `10.20.0.0/16`. A range opens
private-network addresses only: RFC 1918 and unique local addresses. An
IPv4 range has at least 8 bits, and an IPv6 range has at least 32 bits. The
server always refuses loopback, link-local, cloud metadata, unspecified and
multicast addresses, also inside a range. So the URL of a provider cannot
reach an address that these rules refuse.

## Plain connections

The setting `egress.allowPlainConnections` allows a connection with no TLS
to a host that is not a loopback IP address. It is off by default. The same
setting allows an SMTP connection with no TLS.

Set it with one call:

```sh
curl -X PATCH "$SERVER/api/dustpan/v1/settings" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/merge-patch+json" \
  -H 'If-Match: "<revision>"' \
  -d '{"egress.allowPlainConnections": true}'
```

An LDAP bind then sends the password of the service account and the
password of each person over the network with no encryption. Set the
setting only in a trusted network. The change makes the installation less
secure, so it needs a fresh authentication.

OpenDustpan Cloud locks the setting off. It sets the variable
`DUSTPAN_ALLOW_PLAIN_CONNECTIONS` to `false`, so the API refuses a change
with the error code `settings.locked`. The flag `--allow-plain-connections`,
the variable and the file key `egress.allowPlainConnections` lock the
setting the same way.

The setting turns off no check of a connection with TLS. Each certificate
check stays.

## Subject

The subject identifies the person in the server. It must not change when
the person changes a name or an email. The server never uses the DN, the
`uid` or the email as the subject.

| Subject | Directory | Text form |
| --- | --- | --- |
| `entryUUID` | OpenLDAP, 389 Directory Server and others (RFC 4530) | The UUID in lowercase, for example `6ba7b810-9dad-11d1-80b4-00c04fd430c8`. |
| `objectGUID` | Active Directory | The GUID text that Active Directory shows, in lowercase. |
| A named attribute | Any | Its one value as text, 1 to 255 bytes, with no control character. |

For `objectGUID`, the first three groups of the text come from the bytes in
little-endian order, and the last two groups come in the order of the bytes.
The bytes `01 02 03 04 05 06 07 08 09 0a 0b 0c 0d 0e 0f 10` give the text
`04030201-0605-0807-090a-0b0c0d0e0f10`.

The server refuses the sign-in when the entry has no value or two values of
the subject attribute, or when the value is binary or has the wrong form.

## Disabled persons

The disabled rule names an attribute and either a bit mask or a list of
values:

- Active Directory: the attribute `userAccountControl` with the mask `2`.
  The server reads the value as a decimal number. A value that is not a
  number refuses the sign-in. An entry with no value also refuses the
  sign-in, because each entry of Active Directory has this attribute: no
  value can mean that the service account cannot read it.
- 389 Directory Server: the attribute `nsAccountLock` with the value
  `true`. The server compares the values with no case. An entry with no
  value is not disabled.

When the directory reports the person as disabled, the server sends no
password bind. The sign-in service then disables the principal of that
subject. A person that the search does not find gets the same answer as a
wrong password, and the principal stays as it is.

## Groups

| Groups | Settings | What the server reads |
| --- | --- | --- |
| `off` | none | No groups. Memberships do not change. |
| `search` | group base DN, group filter, member attribute, group name attribute | The groups below the group base DN whose member attribute holds the DN of the person. |
| `memberOf` | group base DN, memberOf attribute, group name attribute | The memberOf attribute of the person. A group outside the group base DN is not read. |

For example, for `search`: the group filter `(objectClass=groupOfNames)`,
the member attribute `member`, and the name attribute `cn`. For
`memberOf`: the memberOf attribute `memberOf` and the name attribute `cn`.

The server reads nested groups. It searches again for each group that it
found, and returns each group that the person reaches, as one flat list. It
reads each group once, also when the groups make a cycle.

These limits apply:

- At most 1000 groups for one person, nested groups included. More groups
  refuse the sign-in. The server never cuts the list.
- Each group has one name of 1 to 256 bytes of text.

With groups on, a person with no group cannot sign in. An empty answer can
come from a service account that cannot read the groups, and an empty list
would remove each membership of the person. To let a person with no group
sign in, turn groups off, or put each person in a group. In Active
Directory, the primary group of a user, for example `Domain Users`, is not
in `memberOf`.

### Mappings

The server does not apply the groups of a directory to the memberships of a
person yet. A sign-in reads the groups, but it changes no membership. A person
who signs in keeps the memberships that a person made with the API.

The server can store mappings. A mapping names the sign-in provider, one group
name from the directory and one group of OpenDustpan. The server compares the
name byte for byte: `Eng` and `eng` are two names. No route to read or write a
mapping exists yet. A backup holds the mappings. A restore brings them back
only for a caller with the `server:restore-access` action.

## Limits

| Limit | Value |
| --- | --- |
| Connection and TLS handshake | 10 seconds |
| One LDAP operation | 10 seconds |
| User name | 256 bytes |
| Password | 1024 bytes |
| Groups of one person | 1000 |
| Read of the groups of one sign-in | 30 seconds |

An error of a sign-in never holds the text of the directory or of the
network. The server writes that text to its log, with the provider ID and
the step that failed.
