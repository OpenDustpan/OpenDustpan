# InstallationApi

All URIs are relative to *http://localhost:8080*

| Method | HTTP request | Description |
|------------- | ------------- | -------------|
| [**getCapabilities**](InstallationApi.md#getcapabilities) | **GET** /api/dustpan/v1/capabilities | Read the API version and the optional modules |



## getCapabilities

> Capabilities getCapabilities()

Read the API version and the optional modules

### Example

```ts
import {
  Configuration,
  InstallationApi,
} from '@opendustpan/client';
import type { GetCapabilitiesRequest } from '@opendustpan/client';

async function example() {
  console.log("🚀 Testing @opendustpan/client SDK...");
  const api = new InstallationApi();

  try {
    const data = await api.getCapabilities();
    console.log(data);
  } catch (error) {
    console.error(error);
  }
}

// Run the test
example().catch(console.error);
```

### Parameters

This endpoint does not need any parameter.

### Return type

[**Capabilities**](Capabilities.md)

### Authorization

No authorization required

### HTTP request headers

- **Content-Type**: Not defined
- **Accept**: `application/json`


### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **200** | OK |  -  |
| **0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)

