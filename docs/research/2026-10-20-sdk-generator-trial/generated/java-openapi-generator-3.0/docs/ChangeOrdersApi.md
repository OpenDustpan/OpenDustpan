# ChangeOrdersApi

All URIs are relative to *http://localhost:8080*

| Method | HTTP request | Description |
|------------- | ------------- | -------------|
| [**setChangeOrderVerdict**](ChangeOrdersApi.md#setChangeOrderVerdict) | **POST** /api/dustpan/v1/namespaces/{namespace}/change-orders/{id}/verdict | Approve or reject a change order |
| [**setChangeOrderVerdictWithHttpInfo**](ChangeOrdersApi.md#setChangeOrderVerdictWithHttpInfo) | **POST** /api/dustpan/v1/namespaces/{namespace}/change-orders/{id}/verdict | Approve or reject a change order |



## setChangeOrderVerdict

> ChangeOrder setChangeOrderVerdict(namespace, id, verdict, idempotencyKey)

Approve or reject a change order

### Example

```java
// Import classes:
import org.opendustpan.client.ApiClient;
import org.opendustpan.client.ApiException;
import org.opendustpan.client.Configuration;
import org.opendustpan.client.auth.*;
import org.opendustpan.client.models.*;
import org.opendustpan.client.api.ChangeOrdersApi;

public class Example {
    public static void main(String[] args) {
        ApiClient defaultClient = Configuration.getDefaultApiClient();
        defaultClient.setBasePath("http://localhost:8080");
        
        // Configure HTTP bearer authorization: bearerAuth
        HttpBearerAuth bearerAuth = (HttpBearerAuth) defaultClient.getAuthentication("bearerAuth");
        bearerAuth.setBearerToken("BEARER TOKEN");

        ChangeOrdersApi apiInstance = new ChangeOrdersApi(defaultClient);
        String namespace = "namespace_example"; // String | A namespace name, or - for a read across namespaces.
        String id = "id_example"; // String | 
        Verdict verdict = new Verdict(); // Verdict | 
        String idempotencyKey = "idempotencyKey_example"; // String | 
        try {
            ChangeOrder result = apiInstance.setChangeOrderVerdict(namespace, id, verdict, idempotencyKey);
            System.out.println(result);
        } catch (ApiException e) {
            System.err.println("Exception when calling ChangeOrdersApi#setChangeOrderVerdict");
            System.err.println("Status code: " + e.getCode());
            System.err.println("Reason: " + e.getResponseBody());
            System.err.println("Response headers: " + e.getResponseHeaders());
            e.printStackTrace();
        }
    }
}
```

### Parameters


| Name | Type | Description  | Notes |
|------------- | ------------- | ------------- | -------------|
| **namespace** | **String**| A namespace name, or - for a read across namespaces. | |
| **id** | **String**|  | |
| **verdict** | [**Verdict**](Verdict.md)|  | |
| **idempotencyKey** | **String**|  | [optional] |

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

## setChangeOrderVerdictWithHttpInfo

> ApiResponse<ChangeOrder> setChangeOrderVerdictWithHttpInfo(namespace, id, verdict, idempotencyKey)

Approve or reject a change order

### Example

```java
// Import classes:
import org.opendustpan.client.ApiClient;
import org.opendustpan.client.ApiException;
import org.opendustpan.client.ApiResponse;
import org.opendustpan.client.Configuration;
import org.opendustpan.client.auth.*;
import org.opendustpan.client.models.*;
import org.opendustpan.client.api.ChangeOrdersApi;

public class Example {
    public static void main(String[] args) {
        ApiClient defaultClient = Configuration.getDefaultApiClient();
        defaultClient.setBasePath("http://localhost:8080");
        
        // Configure HTTP bearer authorization: bearerAuth
        HttpBearerAuth bearerAuth = (HttpBearerAuth) defaultClient.getAuthentication("bearerAuth");
        bearerAuth.setBearerToken("BEARER TOKEN");

        ChangeOrdersApi apiInstance = new ChangeOrdersApi(defaultClient);
        String namespace = "namespace_example"; // String | A namespace name, or - for a read across namespaces.
        String id = "id_example"; // String | 
        Verdict verdict = new Verdict(); // Verdict | 
        String idempotencyKey = "idempotencyKey_example"; // String | 
        try {
            ApiResponse<ChangeOrder> response = apiInstance.setChangeOrderVerdictWithHttpInfo(namespace, id, verdict, idempotencyKey);
            System.out.println("Status code: " + response.getStatusCode());
            System.out.println("Response headers: " + response.getHeaders());
            System.out.println("Response body: " + response.getData());
        } catch (ApiException e) {
            System.err.println("Exception when calling ChangeOrdersApi#setChangeOrderVerdict");
            System.err.println("Status code: " + e.getCode());
            System.err.println("Response headers: " + e.getResponseHeaders());
            System.err.println("Reason: " + e.getResponseBody());
            e.printStackTrace();
        }
    }
}
```

### Parameters


| Name | Type | Description  | Notes |
|------------- | ------------- | ------------- | -------------|
| **namespace** | **String**| A namespace name, or - for a read across namespaces. | |
| **id** | **String**|  | |
| **verdict** | [**Verdict**](Verdict.md)|  | |
| **idempotencyKey** | **String**|  | [optional] |

### Return type

ApiResponse<[**ChangeOrder**](ChangeOrder.md)>


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

