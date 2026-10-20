
# Preferences


## Properties

Name | Type
------------ | -------------
`extra` | { [key: string]: any; }
`language` | string
`pinnedUnits` | Array&lt;string&gt;
`theme` | string

## Example

```typescript
import type { Preferences } from '@opendustpan/client'

// TODO: Update the object below with actual values
const example = {
  "extra": null,
  "language": null,
  "pinnedUnits": null,
  "theme": null,
} satisfies Preferences

console.log(example)

// Convert the instance to a JSON string
const exampleJSON: string = JSON.stringify(example)
console.log(exampleJSON)

// Parse the JSON string back to an object
const exampleParsed = JSON.parse(exampleJSON) as Preferences
console.log(exampleParsed)
```

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)


