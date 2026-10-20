# UnitsApi

All URIs are relative to *http://localhost:8080*

| Method | HTTP request | Description |
|------------- | ------------- | -------------|
| [**getUnit**](UnitsApi.md#getunit) | **GET** /api/dustpan/v1/namespaces/{namespace}/units/{unit_id} | Read a unit |
| [**listUnits**](UnitsApi.md#listunits) | **GET** /api/dustpan/v1/namespaces/{namespace}/units | List the units of a namespace |
| [**restoreDeletedUnit**](UnitsApi.md#restoredeletedunit) | **POST** /api/dustpan/v1/namespaces/{namespace}/deleted-units/{unit_id}/restore | Restore a deleted unit |



## getUnit

> Unit getUnit(namespace, unitId)

Read a unit

### Example

```ts
import {
  Configuration,
  UnitsApi,
} from '@opendustpan/client';
import type { GetUnitRequest } from '@opendustpan/client';

async function example() {
  console.log("🚀 Testing @opendustpan/client SDK...");
  const config = new Configuration({ 
    // Configure HTTP bearer authorization: bearerAuth
    accessToken: "YOUR BEARER TOKEN",
  });
  const api = new UnitsApi(config);

  const body = {
    // string | A namespace name, or - for a read across namespaces.
    namespace: namespace_example,
    // string
    unitId: unitId_example,
  } satisfies GetUnitRequest;

  try {
    const data = await api.getUnit(body);
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
| **unitId** | `string` |  | [Defaults to `undefined`] |

### Return type

[**Unit**](Unit.md)

### Authorization

[bearerAuth](../README.md#bearerAuth)

### HTTP request headers

- **Content-Type**: Not defined
- **Accept**: `application/json`


### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **200** | OK |  * ETag -  <br>  |
| **0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)


## listUnits

> UnitList listUnits(namespace, pageSize, pageToken, tool)

List the units of a namespace

### Example

```ts
import {
  Configuration,
  UnitsApi,
} from '@opendustpan/client';
import type { ListUnitsRequest } from '@opendustpan/client';

async function example() {
  console.log("🚀 Testing @opendustpan/client SDK...");
  const config = new Configuration({ 
    // Configure HTTP bearer authorization: bearerAuth
    accessToken: "YOUR BEARER TOKEN",
  });
  const api = new UnitsApi(config);

  const body = {
    // string | A namespace name, or - for a read across namespaces.
    namespace: namespace_example,
    // number (optional)
    pageSize: 56,
    // string (optional)
    pageToken: pageToken_example,
    // Tool (optional)
    tool: ...,
  } satisfies ListUnitsRequest;

  try {
    const data = await api.listUnits(body);
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
| **pageSize** | `number` |  | [Optional] [Defaults to `50`] |
| **pageToken** | `string` |  | [Optional] [Defaults to `undefined`] |
| **tool** | `Tool` |  | [Optional] [Defaults to `undefined`] [Enum: pulumi, opentofu] |

### Return type

[**UnitList**](UnitList.md)

### Authorization

[bearerAuth](../README.md#bearerAuth)

### HTTP request headers

- **Content-Type**: Not defined
- **Accept**: `application/json`


### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **200** | OK |  -  |
| **0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)


## restoreDeletedUnit

> Unit restoreDeletedUnit(namespace, unitId, idempotencyKey)

Restore a deleted unit

### Example

```ts
import {
  Configuration,
  UnitsApi,
} from '@opendustpan/client';
import type { RestoreDeletedUnitRequest } from '@opendustpan/client';

async function example() {
  console.log("🚀 Testing @opendustpan/client SDK...");
  const config = new Configuration({ 
    // Configure HTTP bearer authorization: bearerAuth
    accessToken: "YOUR BEARER TOKEN",
  });
  const api = new UnitsApi(config);

  const body = {
    // string | A namespace name, or - for a read across namespaces.
    namespace: namespace_example,
    // string
    unitId: unitId_example,
    // string (optional)
    idempotencyKey: idempotencyKey_example,
  } satisfies RestoreDeletedUnitRequest;

  try {
    const data = await api.restoreDeletedUnit(body);
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
| **unitId** | `string` |  | [Defaults to `undefined`] |
| **idempotencyKey** | `string` |  | [Optional] [Defaults to `undefined`] |

### Return type

[**Unit**](Unit.md)

### Authorization

[bearerAuth](../README.md#bearerAuth)

### HTTP request headers

- **Content-Type**: Not defined
- **Accept**: `application/json`


### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **200** | OK |  -  |
| **0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)

