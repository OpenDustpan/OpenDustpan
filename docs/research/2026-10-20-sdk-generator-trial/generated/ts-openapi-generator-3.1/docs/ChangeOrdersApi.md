# ChangeOrdersApi

All URIs are relative to *http://localhost:8080*

| Method | HTTP request | Description |
|------------- | ------------- | -------------|
| [**setChangeOrderVerdict**](ChangeOrdersApi.md#setchangeorderverdict) | **POST** /api/dustpan/v1/namespaces/{namespace}/change-orders/{id}/verdict | Approve or reject a change order |



## setChangeOrderVerdict

> ChangeOrder setChangeOrderVerdict(namespace, id, verdict, idempotencyKey)

Approve or reject a change order

### Example

```ts
import {
  Configuration,
  ChangeOrdersApi,
} from '@opendustpan/client';
import type { SetChangeOrderVerdictRequest } from '@opendustpan/client';

async function example() {
  console.log("🚀 Testing @opendustpan/client SDK...");
  const config = new Configuration({ 
    // Configure HTTP bearer authorization: bearerAuth
    accessToken: "YOUR BEARER TOKEN",
  });
  const api = new ChangeOrdersApi(config);

  const body = {
    // string | A namespace name, or - for a read across namespaces.
    namespace: namespace_example,
    // string
    id: id_example,
    // Verdict
    verdict: ...,
    // string (optional)
    idempotencyKey: idempotencyKey_example,
  } satisfies SetChangeOrderVerdictRequest;

  try {
    const data = await api.setChangeOrderVerdict(body);
    console.log(data);
  } catch (error) {
    console.error(error);
  }
}

// Run the test
example().catch(console.error);
```

### Parameters


| Name | Type | Description  | Notes |
|------------- | ------------- | ------------- | -------------|
| **namespace** | `string` | A namespace name, or - for a read across namespaces. | [Defaults to `undefined`] |
| **id** | `string` |  | [Defaults to `undefined`] |
| **verdict** | [Verdict](Verdict.md) |  | |
| **idempotencyKey** | `string` |  | [Optional] [Defaults to `undefined`] |

### Return type

[**ChangeOrder**](ChangeOrder.md)

### Authorization

[bearerAuth](../README.md#bearerAuth)

### HTTP request headers

- **Content-Type**: `application/json`
- **Accept**: `application/json`


### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **200** | OK |  -  |
| **0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)

