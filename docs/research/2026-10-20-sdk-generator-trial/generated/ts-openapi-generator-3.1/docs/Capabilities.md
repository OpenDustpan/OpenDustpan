
# Capabilities


## Properties

Name | Type
------------ | -------------
`apiVersion` | string
`modules` | [Array&lt;Module&gt;](Module.md)

## Example

```typescript
import type { Capabilities } from '@opendustpan/client'

// TODO: Update the object below with actual values
const example = {
  "apiVersion": null,
  "modules": null,
} satisfies Capabilities

console.log(example)

// Convert the instance to a JSON string
const exampleJSON: string = JSON.stringify(example)
console.log(exampleJSON)

// Parse the JSON string back to an object
const exampleParsed = JSON.parse(exampleJSON) as Capabilities
console.log(exampleParsed)
```

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)


