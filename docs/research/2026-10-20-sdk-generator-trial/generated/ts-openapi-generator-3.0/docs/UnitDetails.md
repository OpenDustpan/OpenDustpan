
# UnitDetails


## Properties

Name | Type
------------ | -------------
`project` | string
`stack` | string
`tool` | string
`terraformVersion` | string
`workspace` | string

## Example

```typescript
import type { UnitDetails } from '@opendustpan/client'

// TODO: Update the object below with actual values
const example = {
  "project": null,
  "stack": null,
  "tool": null,
  "terraformVersion": null,
  "workspace": null,
} satisfies UnitDetails

console.log(example)

// Convert the instance to a JSON string
const exampleJSON: string = JSON.stringify(example)
console.log(exampleJSON)

// Parse the JSON string back to an object
const exampleParsed = JSON.parse(exampleJSON) as UnitDetails
console.log(exampleParsed)
```

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)


