# OpenDustpan.Client.Api.ChangeOrdersApi

All URIs are relative to *http://localhost:8080*

| Method | HTTP request | Description |
|--------|--------------|-------------|
| [**SetChangeOrderVerdict**](ChangeOrdersApi.md#setchangeorderverdict) | **POST** /api/dustpan/v1/namespaces/{namespace}/change-orders/{id}/verdict | Approve or reject a change order |

<a id="setchangeorderverdict"></a>
# **SetChangeOrderVerdict**
> ChangeOrder SetChangeOrderVerdict (string varNamespace, string id, Verdict verdict, string idempotencyKey = null)

Approve or reject a change order


### Parameters

| Name | Type | Description | Notes |
|------|------|-------------|-------|
| **varNamespace** | **string** | A namespace name, or - for a read across namespaces. |  |
| **id** | **string** |  |  |
| **verdict** | [**Verdict**](Verdict.md) |  |  |
| **idempotencyKey** | **string** |  | [optional]  |

### Return type

[**ChangeOrder**](ChangeOrder.md)

### Authorization

[bearerAuth](../README.md#bearerAuth)

### HTTP request headers

 - **Content-Type**: application/json
 - **Accept**: application/json


### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **200** | OK |  -  |
| **0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../../README.md#documentation-for-api-endpoints) [[Back to Model list]](../../README.md#documentation-for-models) [[Back to README]](../../README.md)

