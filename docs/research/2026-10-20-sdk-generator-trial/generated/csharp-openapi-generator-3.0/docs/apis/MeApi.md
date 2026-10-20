# OpenDustpan.Client.Api.MeApi

All URIs are relative to *http://localhost:8080*

| Method | HTTP request | Description |
|--------|--------------|-------------|
| [**GetPreferences**](MeApi.md#getpreferences) | **GET** /api/dustpan/v1/me/preferences | Read the preferences of the caller |
| [**UpdatePreferences**](MeApi.md#updatepreferences) | **PATCH** /api/dustpan/v1/me/preferences | Change the preferences of the caller |

<a id="getpreferences"></a>
# **GetPreferences**
> Preferences GetPreferences ()

Read the preferences of the caller


### Parameters
This endpoint does not need any parameter.
### Return type

[**Preferences**](Preferences.md)

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

<a id="updatepreferences"></a>
# **UpdatePreferences**
> Preferences UpdatePreferences (Preferences preferences, string idempotencyKey = null, string ifMatch = null)

Change the preferences of the caller


### Parameters

| Name | Type | Description | Notes |
|------|------|-------------|-------|
| **preferences** | [**Preferences**](Preferences.md) |  |  |
| **idempotencyKey** | **string** |  | [optional]  |
| **ifMatch** | **string** |  | [optional]  |

### Return type

[**Preferences**](Preferences.md)

### Authorization

[bearerAuth](../README.md#bearerAuth)

### HTTP request headers

 - **Content-Type**: application/merge-patch+json
 - **Accept**: application/json


### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **200** | OK |  * ETag -  <br>  |
| **0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../../README.md#documentation-for-api-endpoints) [[Back to Model list]](../../README.md#documentation-for-models) [[Back to README]](../../README.md)

