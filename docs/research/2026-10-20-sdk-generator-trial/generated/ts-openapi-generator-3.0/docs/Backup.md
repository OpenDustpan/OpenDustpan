
# Backup


## Properties

Name | Type
------------ | -------------
`createdAt` | Date
`finishedAt` | Date
`id` | string
`sizeBytes` | number
`state` | [BackupState](BackupState.md)

## Example

```typescript
import type { Backup } from '@opendustpan/client'

// TODO: Update the object below with actual values
const example = {
  "createdAt": null,
  "finishedAt": null,
  "id": null,
  "sizeBytes": null,
  "state": null,
} satisfies Backup

console.log(example)

// Convert the instance to a JSON string
const exampleJSON: string = JSON.stringify(example)
console.log(exampleJSON)

// Parse the JSON string back to an object
const exampleParsed = JSON.parse(exampleJSON) as Backup
console.log(exampleParsed)
```

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)


