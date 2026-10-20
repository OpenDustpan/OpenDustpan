# MeApi

All URIs are relative to *http://localhost:8080*

| Method | HTTP request | Description |
|------------- | ------------- | -------------|
| [**getPreferences**](MeApi.md#getpreferences) | **GET** /api/dustpan/v1/me/preferences | Read the preferences of the caller |
| [**updatePreferences**](MeApi.md#updatepreferences) | **PATCH** /api/dustpan/v1/me/preferences | Change the preferences of the caller |



## getPreferences

> Preferences getPreferences()

Read the preferences of the caller

### Example

```ts
import {
  Configuration,
  MeApi,
} from '@opendustpan/client';
import type { GetPreferencesRequest } from '@opendustpan/client';

async function example() {
  console.log("🚀 Testing @opendustpan/client SDK...");
  const config = new Configuration({ 
    // Configure HTTP bearer authorization: bearerAuth
    accessToken: "YOUR BEARER TOKEN",
  });
  const api = new MeApi(config);

  try {
    const data = await api.getPreferences();
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

[**Preferences**](Preferences.md)

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


## updatePreferences

> Preferences updatePreferences(preferences, idempotencyKey, ifMatch)

Change the preferences of the caller

### Example

```ts
import {
  Configuration,
  MeApi,
} from '@opendustpan/client';
import type { UpdatePreferencesRequest } from '@opendustpan/client';

async function example() {
  console.log("🚀 Testing @opendustpan/client SDK...");
  const config = new Configuration({ 
    // Configure HTTP bearer authorization: bearerAuth
    accessToken: "YOUR BEARER TOKEN",
  });
  const api = new MeApi(config);

  const body = {
    // Preferences
    preferences: ...,
    // string (optional)
    idempotencyKey: idempotencyKey_example,
    // string (optional)
    ifMatch: ifMatch_example,
  } satisfies UpdatePreferencesRequest;

  try {
    const data = await api.updatePreferences(body);
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
| **preferences** | [Preferences](Preferences.md) |  | |
| **idempotencyKey** | `string` |  | [Optional] [Defaults to `undefined`] |
| **ifMatch** | `string` |  | [Optional] [Defaults to `undefined`] |

### Return type

[**Preferences**](Preferences.md)

### Authorization

[bearerAuth](../README.md#bearerAuth)

### HTTP request headers

- **Content-Type**: `application/merge-patch+json`
- **Accept**: `application/json`


### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **200** | OK |  * ETag -  <br>  |
| **0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)

