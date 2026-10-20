# UnitsApi

All URIs are relative to *http://localhost:8080*

| Method | HTTP request | Description |
|------------- | ------------- | -------------|
| [**getUnit**](UnitsApi.md#getUnit) | **GET** /api/dustpan/v1/namespaces/{namespace}/units/{unit_id} | Read a unit |
| [**getUnitWithHttpInfo**](UnitsApi.md#getUnitWithHttpInfo) | **GET** /api/dustpan/v1/namespaces/{namespace}/units/{unit_id} | Read a unit |
| [**listUnits**](UnitsApi.md#listUnits) | **GET** /api/dustpan/v1/namespaces/{namespace}/units | List the units of a namespace |
| [**listUnitsWithHttpInfo**](UnitsApi.md#listUnitsWithHttpInfo) | **GET** /api/dustpan/v1/namespaces/{namespace}/units | List the units of a namespace |
| [**restoreDeletedUnit**](UnitsApi.md#restoreDeletedUnit) | **POST** /api/dustpan/v1/namespaces/{namespace}/deleted-units/{unit_id}/restore | Restore a deleted unit |
| [**restoreDeletedUnitWithHttpInfo**](UnitsApi.md#restoreDeletedUnitWithHttpInfo) | **POST** /api/dustpan/v1/namespaces/{namespace}/deleted-units/{unit_id}/restore | Restore a deleted unit |



## getUnit

> Unit getUnit(namespace, unitId)

Read a unit

### Example

```java
// Import classes:
import org.opendustpan.client.ApiClient;
import org.opendustpan.client.ApiException;
import org.opendustpan.client.Configuration;
import org.opendustpan.client.auth.*;
import org.opendustpan.client.models.*;
import org.opendustpan.client.api.UnitsApi;

public class Example {
    public static void main(String[] args) {
        ApiClient defaultClient = Configuration.getDefaultApiClient();
        defaultClient.setBasePath("http://localhost:8080");
        
        // Configure HTTP bearer authorization: bearerAuth
        HttpBearerAuth bearerAuth = (HttpBearerAuth) defaultClient.getAuthentication("bearerAuth");
        bearerAuth.setBearerToken("BEARER TOKEN");

        UnitsApi apiInstance = new UnitsApi(defaultClient);
        String namespace = "namespace_example"; // String | A namespace name, or - for a read across namespaces.
        String unitId = "unitId_example"; // String | 
        try {
            Unit result = apiInstance.getUnit(namespace, unitId);
            System.out.println(result);
        } catch (ApiException e) {
            System.err.println("Exception when calling UnitsApi#getUnit");
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
| **unitId** | **String**|  | |

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

## getUnitWithHttpInfo

> ApiResponse<Unit> getUnitWithHttpInfo(namespace, unitId)

Read a unit

### Example

```java
// Import classes:
import org.opendustpan.client.ApiClient;
import org.opendustpan.client.ApiException;
import org.opendustpan.client.ApiResponse;
import org.opendustpan.client.Configuration;
import org.opendustpan.client.auth.*;
import org.opendustpan.client.models.*;
import org.opendustpan.client.api.UnitsApi;

public class Example {
    public static void main(String[] args) {
        ApiClient defaultClient = Configuration.getDefaultApiClient();
        defaultClient.setBasePath("http://localhost:8080");
        
        // Configure HTTP bearer authorization: bearerAuth
        HttpBearerAuth bearerAuth = (HttpBearerAuth) defaultClient.getAuthentication("bearerAuth");
        bearerAuth.setBearerToken("BEARER TOKEN");

        UnitsApi apiInstance = new UnitsApi(defaultClient);
        String namespace = "namespace_example"; // String | A namespace name, or - for a read across namespaces.
        String unitId = "unitId_example"; // String | 
        try {
            ApiResponse<Unit> response = apiInstance.getUnitWithHttpInfo(namespace, unitId);
            System.out.println("Status code: " + response.getStatusCode());
            System.out.println("Response headers: " + response.getHeaders());
            System.out.println("Response body: " + response.getData());
        } catch (ApiException e) {
            System.err.println("Exception when calling UnitsApi#getUnit");
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
| **unitId** | **String**|  | |

### Return type

ApiResponse<[**Unit**](Unit.md)>


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


## listUnits

> UnitList listUnits(namespace, pageSize, pageToken, tool)

List the units of a namespace

### Example

```java
// Import classes:
import org.opendustpan.client.ApiClient;
import org.opendustpan.client.ApiException;
import org.opendustpan.client.Configuration;
import org.opendustpan.client.auth.*;
import org.opendustpan.client.models.*;
import org.opendustpan.client.api.UnitsApi;

public class Example {
    public static void main(String[] args) {
        ApiClient defaultClient = Configuration.getDefaultApiClient();
        defaultClient.setBasePath("http://localhost:8080");
        
        // Configure HTTP bearer authorization: bearerAuth
        HttpBearerAuth bearerAuth = (HttpBearerAuth) defaultClient.getAuthentication("bearerAuth");
        bearerAuth.setBearerToken("BEARER TOKEN");

        UnitsApi apiInstance = new UnitsApi(defaultClient);
        String namespace = "namespace_example"; // String | A namespace name, or - for a read across namespaces.
        Integer pageSize = 50; // Integer | 
        String pageToken = "pageToken_example"; // String | 
        Tool tool = Tool.fromValue("pulumi"); // Tool | 
        try {
            UnitList result = apiInstance.listUnits(namespace, pageSize, pageToken, tool);
            System.out.println(result);
        } catch (ApiException e) {
            System.err.println("Exception when calling UnitsApi#listUnits");
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
| **pageSize** | **Integer**|  | [optional] [default to 50] |
| **pageToken** | **String**|  | [optional] |
| **tool** | [**Tool**](.md)|  | [optional] [enum: pulumi, opentofu] |

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

## listUnitsWithHttpInfo

> ApiResponse<UnitList> listUnitsWithHttpInfo(namespace, pageSize, pageToken, tool)

List the units of a namespace

### Example

```java
// Import classes:
import org.opendustpan.client.ApiClient;
import org.opendustpan.client.ApiException;
import org.opendustpan.client.ApiResponse;
import org.opendustpan.client.Configuration;
import org.opendustpan.client.auth.*;
import org.opendustpan.client.models.*;
import org.opendustpan.client.api.UnitsApi;

public class Example {
    public static void main(String[] args) {
        ApiClient defaultClient = Configuration.getDefaultApiClient();
        defaultClient.setBasePath("http://localhost:8080");
        
        // Configure HTTP bearer authorization: bearerAuth
        HttpBearerAuth bearerAuth = (HttpBearerAuth) defaultClient.getAuthentication("bearerAuth");
        bearerAuth.setBearerToken("BEARER TOKEN");

        UnitsApi apiInstance = new UnitsApi(defaultClient);
        String namespace = "namespace_example"; // String | A namespace name, or - for a read across namespaces.
        Integer pageSize = 50; // Integer | 
        String pageToken = "pageToken_example"; // String | 
        Tool tool = Tool.fromValue("pulumi"); // Tool | 
        try {
            ApiResponse<UnitList> response = apiInstance.listUnitsWithHttpInfo(namespace, pageSize, pageToken, tool);
            System.out.println("Status code: " + response.getStatusCode());
            System.out.println("Response headers: " + response.getHeaders());
            System.out.println("Response body: " + response.getData());
        } catch (ApiException e) {
            System.err.println("Exception when calling UnitsApi#listUnits");
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
| **pageSize** | **Integer**|  | [optional] [default to 50] |
| **pageToken** | **String**|  | [optional] |
| **tool** | [**Tool**](.md)|  | [optional] [enum: pulumi, opentofu] |

### Return type

ApiResponse<[**UnitList**](UnitList.md)>


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


## restoreDeletedUnit

> Unit restoreDeletedUnit(namespace, unitId, idempotencyKey)

Restore a deleted unit

### Example

```java
// Import classes:
import org.opendustpan.client.ApiClient;
import org.opendustpan.client.ApiException;
import org.opendustpan.client.Configuration;
import org.opendustpan.client.auth.*;
import org.opendustpan.client.models.*;
import org.opendustpan.client.api.UnitsApi;

public class Example {
    public static void main(String[] args) {
        ApiClient defaultClient = Configuration.getDefaultApiClient();
        defaultClient.setBasePath("http://localhost:8080");
        
        // Configure HTTP bearer authorization: bearerAuth
        HttpBearerAuth bearerAuth = (HttpBearerAuth) defaultClient.getAuthentication("bearerAuth");
        bearerAuth.setBearerToken("BEARER TOKEN");

        UnitsApi apiInstance = new UnitsApi(defaultClient);
        String namespace = "namespace_example"; // String | A namespace name, or - for a read across namespaces.
        String unitId = "unitId_example"; // String | 
        String idempotencyKey = "idempotencyKey_example"; // String | 
        try {
            Unit result = apiInstance.restoreDeletedUnit(namespace, unitId, idempotencyKey);
            System.out.println(result);
        } catch (ApiException e) {
            System.err.println("Exception when calling UnitsApi#restoreDeletedUnit");
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
| **unitId** | **String**|  | |
| **idempotencyKey** | **String**|  | [optional] |

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

## restoreDeletedUnitWithHttpInfo

> ApiResponse<Unit> restoreDeletedUnitWithHttpInfo(namespace, unitId, idempotencyKey)

Restore a deleted unit

### Example

```java
// Import classes:
import org.opendustpan.client.ApiClient;
import org.opendustpan.client.ApiException;
import org.opendustpan.client.ApiResponse;
import org.opendustpan.client.Configuration;
import org.opendustpan.client.auth.*;
import org.opendustpan.client.models.*;
import org.opendustpan.client.api.UnitsApi;

public class Example {
    public static void main(String[] args) {
        ApiClient defaultClient = Configuration.getDefaultApiClient();
        defaultClient.setBasePath("http://localhost:8080");
        
        // Configure HTTP bearer authorization: bearerAuth
        HttpBearerAuth bearerAuth = (HttpBearerAuth) defaultClient.getAuthentication("bearerAuth");
        bearerAuth.setBearerToken("BEARER TOKEN");

        UnitsApi apiInstance = new UnitsApi(defaultClient);
        String namespace = "namespace_example"; // String | A namespace name, or - for a read across namespaces.
        String unitId = "unitId_example"; // String | 
        String idempotencyKey = "idempotencyKey_example"; // String | 
        try {
            ApiResponse<Unit> response = apiInstance.restoreDeletedUnitWithHttpInfo(namespace, unitId, idempotencyKey);
            System.out.println("Status code: " + response.getStatusCode());
            System.out.println("Response headers: " + response.getHeaders());
            System.out.println("Response body: " + response.getData());
        } catch (ApiException e) {
            System.err.println("Exception when calling UnitsApi#restoreDeletedUnit");
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
| **unitId** | **String**|  | |
| **idempotencyKey** | **String**|  | [optional] |

### Return type

ApiResponse<[**Unit**](Unit.md)>


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

