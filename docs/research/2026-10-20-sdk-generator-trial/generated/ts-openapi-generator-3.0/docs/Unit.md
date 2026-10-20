
# Unit


## Properties

Name | Type
------------ | -------------
`details` | [UnitDetails](UnitDetails.md)
`labels` | { [key: string]: string; }
`lastChange` | Date
`lock` | [Lock](Lock.md)
`name` | string
`namespace` | string
`resourceCount` | number
`tool` | [Tool](Tool.md)
`unitId` | string

## Example

```typescript
import type { Unit } from '@opendustpan/client'

// TODO: Update the object below with actual values
const example = {
  "details": null,
  "labels": null,
  "lastChange": null,
  "lock": null,
  "name": null,
  "namespace": null,
  "resourceCount": null,
  "tool": null,
  "unitId": null,
} satisfies Unit

console.log(example)

// Convert the instance to a JSON string
const exampleJSON: string = JSON.stringify(example)
console.log(exampleJSON)

// Parse the JSON string back to an object
const exampleParsed = JSON.parse(exampleJSON) as Unit
console.log(exampleParsed)
```

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)


