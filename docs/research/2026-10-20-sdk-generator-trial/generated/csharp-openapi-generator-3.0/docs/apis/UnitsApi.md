# OpenDustpan.Client.Api.UnitsApi

All URIs are relative to *http://localhost:8080*

| Method | HTTP request | Description |
|--------|--------------|-------------|
| [**GetUnit**](UnitsApi.md#getunit) | **GET** /api/dustpan/v1/namespaces/{namespace}/units/{unit_id} | Read a unit |
| [**ListUnits**](UnitsApi.md#listunits) | **GET** /api/dustpan/v1/namespaces/{namespace}/units | List the units of a namespace |
| [**RestoreDeletedUnit**](UnitsApi.md#restoredeletedunit) | **POST** /api/dustpan/v1/namespaces/{namespace}/deleted-units/{unit_id}/restore | Restore a deleted unit |

<a id="getunit"></a>
# **GetUnit**
> Unit GetUnit (string varNamespace, string unitId)

Read a unit


### Parameters

| Name | Type | Description | Notes |
|------|------|-------------|-------|
| **varNamespace** | **string** | A namespace name, or - for a read across namespaces. |  |
| **unitId** | **string** |  |  |

### Return type

[**Unit**](Unit.md)

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

<a id="listunits"></a>
# **ListUnits**
> UnitList ListUnits (string varNamespace, int pageSize = null, string pageToken = null, Tool tool = null)

List the units of a namespace


### Parameters

| Name | Type | Description | Notes |
|------|------|-------------|-------|
| **varNamespace** | **string** | A namespace name, or - for a read across namespaces. |  |
| **pageSize** | **int** |  | [optional] [default to 50] |
| **pageToken** | **string** |  | [optional]  |
| **tool** | **Tool** |  | [optional]  |

### Return type

[**UnitList**](UnitList.md)

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

<a id="restoredeletedunit"></a>
# **RestoreDeletedUnit**
> Unit RestoreDeletedUnit (string varNamespace, string unitId, string idempotencyKey = null)

Restore a deleted unit


### Parameters

| Name | Type | Description | Notes |
|------|------|-------------|-------|
| **varNamespace** | **string** | A namespace name, or - for a read across namespaces. |  |
| **unitId** | **string** |  |  |
| **idempotencyKey** | **string** |  | [optional]  |

### Return type

[**Unit**](Unit.md)

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

