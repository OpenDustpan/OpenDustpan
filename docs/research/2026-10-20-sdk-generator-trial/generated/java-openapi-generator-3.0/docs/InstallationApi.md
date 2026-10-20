# InstallationApi

All URIs are relative to *http://localhost:8080*

| Method | HTTP request | Description |
|------------- | ------------- | -------------|
| [**getCapabilities**](InstallationApi.md#getCapabilities) | **GET** /api/dustpan/v1/capabilities | Read the API version and the optional modules |
| [**getCapabilitiesWithHttpInfo**](InstallationApi.md#getCapabilitiesWithHttpInfo) | **GET** /api/dustpan/v1/capabilities | Read the API version and the optional modules |



## getCapabilities

> Capabilities getCapabilities()

Read the API version and the optional modules

### Example

```java
// Import classes:
import org.opendustpan.client.ApiClient;
import org.opendustpan.client.ApiException;
import org.opendustpan.client.Configuration;
import org.opendustpan.client.models.*;
import org.opendustpan.client.api.InstallationApi;

public class Example {
    public static void main(String[] args) {
        ApiClient defaultClient = Configuration.getDefaultApiClient();
        defaultClient.setBasePath("http://localhost:8080");

        InstallationApi apiInstance = new InstallationApi(defaultClient);
        try {
            Capabilities result = apiInstance.getCapabilities();
            System.out.println(result);
        } catch (ApiException e) {
            System.err.println("Exception when calling InstallationApi#getCapabilities");
            System.err.println("Status code: " + e.getCode());
            System.err.println("Reason: " + e.getResponseBody());
            System.err.println("Response headers: " + e.getResponseHeaders());
            e.printStackTrace();
        }
    }
}
```

### Parameters

This endpoint does not need any parameter.

### Return type

[**Capabilities**](Capabilities.md)


### Authorization

No authorization required

### HTTP request headers

- **Content-Type**: Not defined
- **Accept**: application/json

### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **200** | OK |  -  |
| **0** | An error |  -  |

## getCapabilitiesWithHttpInfo

> ApiResponse<Capabilities> getCapabilitiesWithHttpInfo()

Read the API version and the optional modules

### Example

```java
// Import classes:
import org.opendustpan.client.ApiClient;
import org.opendustpan.client.ApiException;
import org.opendustpan.client.ApiResponse;
import org.opendustpan.client.Configuration;
import org.opendustpan.client.models.*;
import org.opendustpan.client.api.InstallationApi;

public class Example {
    public static void main(String[] args) {
        ApiClient defaultClient = Configuration.getDefaultApiClient();
        defaultClient.setBasePath("http://localhost:8080");

        InstallationApi apiInstance = new InstallationApi(defaultClient);
        try {
            ApiResponse<Capabilities> response = apiInstance.getCapabilitiesWithHttpInfo();
            System.out.println("Status code: " + response.getStatusCode());
            System.out.println("Response headers: " + response.getHeaders());
            System.out.println("Response body: " + response.getData());
        } catch (ApiException e) {
            System.err.println("Exception when calling InstallationApi#getCapabilities");
            System.err.println("Status code: " + e.getCode());
            System.err.println("Response headers: " + e.getResponseHeaders());
            System.err.println("Reason: " + e.getResponseBody());
            e.printStackTrace();
        }
    }
}
```

### Parameters

This endpoint does not need any parameter.

### Return type

ApiResponse<[**Capabilities**](Capabilities.md)>


### Authorization

No authorization required

### HTTP request headers

- **Content-Type**: Not defined
- **Accept**: application/json

### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **200** | OK |  -  |
| **0** | An error |  -  |

