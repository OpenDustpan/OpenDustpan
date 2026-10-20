# BackupsApi

All URIs are relative to *http://localhost:8080*

| Method | HTTP request | Description |
|------------- | ------------- | -------------|
| [**checkBackup**](BackupsApi.md#checkbackup) | **POST** /api/dustpan/v1/backups/{id}/check | Check a backup |
| [**createBackup**](BackupsApi.md#createbackup) | **POST** /api/dustpan/v1/backups | Start a backup |
| [**deleteBackup**](BackupsApi.md#deletebackup) | **DELETE** /api/dustpan/v1/backups/{id} | Delete a backup |
| [**getBackup**](BackupsApi.md#getbackup) | **GET** /api/dustpan/v1/backups/{id} | Read a backup |
| [**listBackups**](BackupsApi.md#listbackups) | **GET** /api/dustpan/v1/backups | List backups |



## checkBackup

> BackupCheck checkBackup(id, idempotencyKey)

Check a backup

### Example

```ts
import {
  Configuration,
  BackupsApi,
} from '@opendustpan/client';
import type { CheckBackupRequest } from '@opendustpan/client';

async function example() {
  console.log("🚀 Testing @opendustpan/client SDK...");
  const config = new Configuration({ 
    // Configure HTTP bearer authorization: bearerAuth
    accessToken: "YOUR BEARER TOKEN",
  });
  const api = new BackupsApi(config);

  const body = {
    // string
    id: id_example,
    // string (optional)
    idempotencyKey: idempotencyKey_example,
  } satisfies CheckBackupRequest;

  try {
    const data = await api.checkBackup(body);
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
| **id** | `string` |  | [Defaults to `undefined`] |
| **idempotencyKey** | `string` |  | [Optional] [Defaults to `undefined`] |

### Return type

[**BackupCheck**](BackupCheck.md)

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


## createBackup

> Backup createBackup(idempotencyKey, backupCreate)

Start a backup

### Example

```ts
import {
  Configuration,
  BackupsApi,
} from '@opendustpan/client';
import type { CreateBackupRequest } from '@opendustpan/client';

async function example() {
  console.log("🚀 Testing @opendustpan/client SDK...");
  const config = new Configuration({ 
    // Configure HTTP bearer authorization: bearerAuth
    accessToken: "YOUR BEARER TOKEN",
  });
  const api = new BackupsApi(config);

  const body = {
    // string (optional)
    idempotencyKey: idempotencyKey_example,
    // BackupCreate (optional)
    backupCreate: ...,
  } satisfies CreateBackupRequest;

  try {
    const data = await api.createBackup(body);
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
| **idempotencyKey** | `string` |  | [Optional] [Defaults to `undefined`] |
| **backupCreate** | [BackupCreate](BackupCreate.md) |  | [Optional] |

### Return type

[**Backup**](Backup.md)

### Authorization

[bearerAuth](../README.md#bearerAuth)

### HTTP request headers

- **Content-Type**: `application/json`
- **Accept**: `application/json`


### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **202** | Accepted |  * ETag -  <br>  |
| **0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)


## deleteBackup

> deleteBackup(id, ifMatch, idempotencyKey)

Delete a backup

### Example

```ts
import {
  Configuration,
  BackupsApi,
} from '@opendustpan/client';
import type { DeleteBackupRequest } from '@opendustpan/client';

async function example() {
  console.log("🚀 Testing @opendustpan/client SDK...");
  const config = new Configuration({ 
    // Configure HTTP bearer authorization: bearerAuth
    accessToken: "YOUR BEARER TOKEN",
  });
  const api = new BackupsApi(config);

  const body = {
    // string
    id: id_example,
    // string
    ifMatch: ifMatch_example,
    // string (optional)
    idempotencyKey: idempotencyKey_example,
  } satisfies DeleteBackupRequest;

  try {
    const data = await api.deleteBackup(body);
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
| **id** | `string` |  | [Defaults to `undefined`] |
| **ifMatch** | `string` |  | [Defaults to `undefined`] |
| **idempotencyKey** | `string` |  | [Optional] [Defaults to `undefined`] |

### Return type

`void` (Empty response body)

### Authorization

[bearerAuth](../README.md#bearerAuth)

### HTTP request headers

- **Content-Type**: Not defined
- **Accept**: `application/json`


### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **204** | Deleted |  -  |
| **0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)


## getBackup

> Backup getBackup(id)

Read a backup

### Example

```ts
import {
  Configuration,
  BackupsApi,
} from '@opendustpan/client';
import type { GetBackupRequest } from '@opendustpan/client';

async function example() {
  console.log("🚀 Testing @opendustpan/client SDK...");
  const config = new Configuration({ 
    // Configure HTTP bearer authorization: bearerAuth
    accessToken: "YOUR BEARER TOKEN",
  });
  const api = new BackupsApi(config);

  const body = {
    // string
    id: id_example,
  } satisfies GetBackupRequest;

  try {
    const data = await api.getBackup(body);
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
| **id** | `string` |  | [Defaults to `undefined`] |

### Return type

[**Backup**](Backup.md)

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


## listBackups

> BackupList listBackups(pageSize, pageToken)

List backups

### Example

```ts
import {
  Configuration,
  BackupsApi,
} from '@opendustpan/client';
import type { ListBackupsRequest } from '@opendustpan/client';

async function example() {
  console.log("🚀 Testing @opendustpan/client SDK...");
  const config = new Configuration({ 
    // Configure HTTP bearer authorization: bearerAuth
    accessToken: "YOUR BEARER TOKEN",
  });
  const api = new BackupsApi(config);

  const body = {
    // number (optional)
    pageSize: 56,
    // string (optional)
    pageToken: pageToken_example,
  } satisfies ListBackupsRequest;

  try {
    const data = await api.listBackups(body);
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
| **pageSize** | `number` |  | [Optional] [Defaults to `50`] |
| **pageToken** | `string` |  | [Optional] [Defaults to `undefined`] |

### Return type

[**BackupList**](BackupList.md)

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

