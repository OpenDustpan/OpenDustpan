
# ModelError

The error shape of the tool-neutral API. code is the HTTP status.

## Properties

Name | Type
------------ | -------------
`code` | number
`errorArgs` | [{ [key: string]: ErrorErrorArgsValue; }](ErrorErrorArgsValue.md)
`errorCode` | string
`message` | string

## Example

```typescript
import type { ModelError } from '@opendustpan/client'

// TODO: Update the object below with actual values
const example = {
  "code": null,
  "errorArgs": null,
  "errorCode": null,
  "message": null,
} satisfies ModelError

console.log(example)

// Convert the instance to a JSON string
const exampleJSON: string = JSON.stringify(example)
console.log(exampleJSON)

// Parse the JSON string back to an object
const exampleParsed = JSON.parse(exampleJSON) as ModelError
console.log(exampleParsed)
```

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)


