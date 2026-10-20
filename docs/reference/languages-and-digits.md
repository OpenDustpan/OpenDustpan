# Languages and digits

This page describes the languages that OpenDustpan supports, the `digits`
setting, and how the server formats numbers and chooses plural forms.

## Languages

The console shows 33 languages. The five newest languages are:

| Tag | Language | Direction |
| --- | --- | --- |
| `af` | Afrikaans | Left to right |
| `ar` | Arabic (Modern Standard Arabic) | Right to left |
| `he` | Hebrew | Right to left |
| `uk` | Ukrainian | Left to right |
| `ru` | Russian | Left to right |

One `ar` language serves all Arabic regions. A regional format, for example
`ar-EG` or `ar-SA`, comes from the formatting locale, not from the language.

## Check of a tag

The server checks only the shape of a language tag. A tag is valid when it is
a well-formed BCP 47 tag of 2 to 35 characters, and it has only ASCII
letters, digits and hyphens. The server does not change a tag to make it
valid. It refuses a tag that is not valid, for example `ar_EG`, `ar EG` or an
empty text.

A formatting locale has the same shape. It cannot have a `-u-` extension or
a `-x-` extension. To change the digits, the clock or the first day of the
week, use the settings for them. A tag such as `ar-u-nu-latn` is refused.

## Digits

The `digits` setting selects the digits of numbers:

| Value | Digits | Example |
| --- | --- | --- |
| `latn` | Western digits, 0 to 9 | `1,204` |
| `arab` | Arabic-Indic digits | `١٬٢٠٤` |
| `null` | The formatting locale decides | |

Any other value is refused, and the error names the valid values:
`digits must be latn, arab or null`.

When `digits` is `null`, the formatting locale decides:

- Arabic uses Arabic-Indic digits, except in the regions `AE`, `DZ`, `EH`,
  `LY`, `MA` and `TN`, which use Western digits.
- Every other language that this page lists uses Western digits.

A `digits` value that is not `null` overrides the formatting locale in every
language. For example, `en-US` with `arab` gives `١٬٢٠٤`, and `ar-EG` with
`latn` gives `1,204`.

The separators come from the formatting locale. The ISO 8601 date style
always uses Western digits. Names, IDs, versions, addresses and the text of a
plan keep their own digits.

## Plural forms

The language of the text, not the formatting locale, sets the plural form of a
count. The categories are `zero`, `one`, `two`, `few`, `many` and `other`.

| Language | Categories | Examples |
| --- | --- | --- |
| Afrikaans | `one`, `other` | 1 is `one`. 0 and 2 are `other`. |
| Russian, Ukrainian | `one`, `few`, `many`, `other` | 1, 21 and 101 are `one`. 2 to 4 and 22 are `few`. 0, 5 to 20 and 100 are `many`. |
| Arabic | `zero`, `one`, `two`, `few`, `many`, `other` | 0 is `zero`. 1 is `one`. 2 is `two`. 3 to 10 are `few`. 11 to 99 are `many`. 100 to 102 are `other`. |
| Hebrew | `one`, `two`, `other` | 1 is `one`. 2 is `two`. |

A negative count has the category of its absolute value.
