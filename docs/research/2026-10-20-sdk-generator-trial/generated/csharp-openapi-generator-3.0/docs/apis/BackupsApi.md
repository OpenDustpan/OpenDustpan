# OpenDustpan.Client.Api.BackupsApi

All URIs are relative to *http://localhost:8080*

| Method | HTTP request | Description |
|--------|--------------|-------------|
| [**CheckBackup**](BackupsApi.md#checkbackup) | **POST** /api/dustpan/v1/backups/{id}/check | Check a backup |
| [**CreateBackup**](BackupsApi.md#createbackup) | **POST** /api/dustpan/v1/backups | Start a backup |
| [**DeleteBackup**](BackupsApi.md#deletebackup) | **DELETE** /api/dustpan/v1/backups/{id} | Delete a backup |
| [**GetBackup**](BackupsApi.md#getbackup) | **GET** /api/dustpan/v1/backups/{id} | Read a backup |
| [**ListBackups**](BackupsApi.md#listbackups) | **GET** /api/dustpan/v1/backups | List backups |

<a id="checkbackup"></a>
# **CheckBackup**
> BackupCheck CheckBackup (string id, string idempotencyKey = null)

Check a backup


### Parameters

| Name | Type | Description | Notes |
|------|------|-------------|-------|
| **id** | **string** |  |  |
| **idempotencyKey** | **string** |  | [optional]  |

### Return type

[**BackupCheck**](BackupCheck.md)

### Authorization

[bearerAuth](../README.md#bearerAuth)

### HTTP request headers

 - **Content-Type**: Not defined
 - **Accept**: application/json


### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **200** | OK |  -  |
| **0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../../README.md#documentation-for-api-endpoints) [[Back to Model list]](../../README.md#documentation-for-models) [[Back to README]](../../README.md)

<a id="createbackup"></a>
# **CreateBackup**
> Backup CreateBackup (string idempotencyKey = null, BackupCreate backupCreate = null)

Start a backup


### Parameters

| Name | Type | Description | Notes |
|------|------|-------------|-------|
| **idempotencyKey** | **string** |  | [optional]  |
| **backupCreate** | [**BackupCreate**](BackupCreate.md) |  | [optional]  |

### Return type

[**Backup**](Backup.md)

### Authorization

[bearerAuth](../README.md#bearerAuth)

### HTTP request headers

 - **Content-Type**: application/json
 - **Accept**: application/json


### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **202** | Accepted |  * ETag -  <br>  |
| **0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../../README.md#documentation-for-api-endpoints) [[Back to Model list]](../../README.md#documentation-for-models) [[Back to README]](../../README.md)

<a id="deletebackup"></a>
# **DeleteBackup**
> void DeleteBackup (string id, string ifMatch, string idempotencyKey = null)

Delete a backup


### Parameters

| Name | Type | Description | Notes |
|------|------|-------------|-------|
| **id** | **string** |  |  |
| **ifMatch** | **string** |  |  |
| **idempotencyKey** | **string** |  | [optional]  |

### Return type

void (empty response body)

### Authorization

[bearerAuth](../README.md#bearerAuth)

### HTTP request headers

 - **Content-Type**: Not defined
 - **Accept**: application/json


### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **204** | Deleted |  -  |
| **0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../../README.md#documentation-for-api-endpoints) [[Back to Model list]](../../README.md#documentation-for-models) [[Back to README]](../../README.md)

<a id="getbackup"></a>
# **GetBackup**
> Backup GetBackup (string id)

Read a backup


### Parameters

| Name | Type | Description | Notes |
|------|------|-------------|-------|
| **id** | **string** |  |  |

### Return type

[**Backup**](Backup.md)

### Authorization

[bearerAuth](../README.md#bearerAuth)

### HTTP request headers

 - **Content-Type**: Not defined
 - **Accept**: application/json


### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **200** | OK |  * ETag -  <br>  |
| **0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../../README.md#documentation-for-api-endpoints) [[Back to Model list]](../../README.md#documentation-for-models) [[Back to README]](../../README.md)

<a id="listbackups"></a>
# **ListBackups**
> BackupList ListBackups (int pageSize = null, string pageToken = null)

List backups


### Parameters

| Name | Type | Description | Notes |
|------|------|-------------|-------|
| **pageSize** | **int** |  | [optional] [default to 50] |
| **pageToken** | **string** |  | [optional]  |

### Return type

[**BackupList**](BackupList.md)

### Authorization

[bearerAuth](../README.md#bearerAuth)

### HTTP request headers

 - **Content-Type**: Not defined
 - **Accept**: application/json


### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **200** | OK |  -  |
| **0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../../README.md#documentation-for-api-endpoints) [[Back to Model list]](../../README.md#documentation-for-models) [[Back to README]](../../README.md)

