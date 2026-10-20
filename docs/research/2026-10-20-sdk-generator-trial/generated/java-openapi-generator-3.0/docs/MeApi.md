# MeApi

All URIs are relative to *http://localhost:8080*

| Method | HTTP request | Description |
|------------- | ------------- | -------------|
| [**getPreferences**](MeApi.md#getPreferences) | **GET** /api/dustpan/v1/me/preferences | Read the preferences of the caller |
| [**getPreferencesWithHttpInfo**](MeApi.md#getPreferencesWithHttpInfo) | **GET** /api/dustpan/v1/me/preferences | Read the preferences of the caller |
| [**updatePreferences**](MeApi.md#updatePreferences) | **PATCH** /api/dustpan/v1/me/preferences | Change the preferences of the caller |
| [**updatePreferencesWithHttpInfo**](MeApi.md#updatePreferencesWithHttpInfo) | **PATCH** /api/dustpan/v1/me/preferences | Change the preferences of the caller |



## getPreferences

> Preferences getPreferences()

Read the preferences of the caller

### Example

```java
// Import classes:
import org.opendustpan.client.ApiClient;
import org.opendustpan.client.ApiException;
import org.opendustpan.client.Configuration;
import org.opendustpan.client.auth.*;
import org.opendustpan.client.models.*;
import org.opendustpan.client.api.MeApi;

public class Example {
    public static void main(String[] args) {
        ApiClient defaultClient = Configuration.getDefaultApiClient();
        defaultClient.setBasePath("http://localhost:8080");
        
        // Configure HTTP bearer authorization: bearerAuth
        HttpBearerAuth bearerAuth = (HttpBearerAuth) defaultClient.getAuthentication("bearerAuth");
        bearerAuth.setBearerToken("BEARER TOKEN");

        MeApi apiInstance = new MeApi(defaultClient);
        try {
            Preferences result = apiInstance.getPreferences();
            System.out.println(result);
        } catch (ApiException e) {
            System.err.println("Exception when calling MeApi#getPreferences");
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

## getPreferencesWithHttpInfo

> ApiResponse<Preferences> getPreferencesWithHttpInfo()

Read the preferences of the caller

### Example

```java
// Import classes:
import org.opendustpan.client.ApiClient;
import org.opendustpan.client.ApiException;
import org.opendustpan.client.ApiResponse;
import org.opendustpan.client.Configuration;
import org.opendustpan.client.auth.*;
import org.opendustpan.client.models.*;
import org.opendustpan.client.api.MeApi;

public class Example {
    public static void main(String[] args) {
        ApiClient defaultClient = Configuration.getDefaultApiClient();
        defaultClient.setBasePath("http://localhost:8080");
        
        // Configure HTTP bearer authorization: bearerAuth
        HttpBearerAuth bearerAuth = (HttpBearerAuth) defaultClient.getAuthentication("bearerAuth");
        bearerAuth.setBearerToken("BEARER TOKEN");

        MeApi apiInstance = new MeApi(defaultClient);
        try {
            ApiResponse<Preferences> response = apiInstance.getPreferencesWithHttpInfo();
            System.out.println("Status code: " + response.getStatusCode());
            System.out.println("Response headers: " + response.getHeaders());
            System.out.println("Response body: " + response.getData());
        } catch (ApiException e) {
            System.err.println("Exception when calling MeApi#getPreferences");
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

ApiResponse<[**Preferences**](Preferences.md)>


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


## updatePreferences

> Preferences updatePreferences(preferences, idempotencyKey, ifMatch)

Change the preferences of the caller

### Example

```java
// Import classes:
import org.opendustpan.client.ApiClient;
import org.opendustpan.client.ApiException;
import org.opendustpan.client.Configuration;
import org.opendustpan.client.auth.*;
import org.opendustpan.client.models.*;
import org.opendustpan.client.api.MeApi;

public class Example {
    public static void main(String[] args) {
        ApiClient defaultClient = Configuration.getDefaultApiClient();
        defaultClient.setBasePath("http://localhost:8080");
        
        // Configure HTTP bearer authorization: bearerAuth
        HttpBearerAuth bearerAuth = (HttpBearerAuth) defaultClient.getAuthentication("bearerAuth");
        bearerAuth.setBearerToken("BEARER TOKEN");

        MeApi apiInstance = new MeApi(defaultClient);
        Preferences preferences = new Preferences(); // Preferences | 
        String idempotencyKey = "idempotencyKey_example"; // String | 
        String ifMatch = "ifMatch_example"; // String | 
        try {
            Preferences result = apiInstance.updatePreferences(preferences, idempotencyKey, ifMatch);
            System.out.println(result);
        } catch (ApiException e) {
            System.err.println("Exception when calling MeApi#updatePreferences");
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
| **preferences** | [**Preferences**](Preferences.md)|  | |
| **idempotencyKey** | **String**|  | [optional] |
| **ifMatch** | **String**|  | [optional] |

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

## updatePreferencesWithHttpInfo

> ApiResponse<Preferences> updatePreferencesWithHttpInfo(preferences, idempotencyKey, ifMatch)

Change the preferences of the caller

### Example

```java
// Import classes:
import org.opendustpan.client.ApiClient;
import org.opendustpan.client.ApiException;
import org.opendustpan.client.ApiResponse;
import org.opendustpan.client.Configuration;
import org.opendustpan.client.auth.*;
import org.opendustpan.client.models.*;
import org.opendustpan.client.api.MeApi;

public class Example {
    public static void main(String[] args) {
        ApiClient defaultClient = Configuration.getDefaultApiClient();
        defaultClient.setBasePath("http://localhost:8080");
        
        // Configure HTTP bearer authorization: bearerAuth
        HttpBearerAuth bearerAuth = (HttpBearerAuth) defaultClient.getAuthentication("bearerAuth");
        bearerAuth.setBearerToken("BEARER TOKEN");

        MeApi apiInstance = new MeApi(defaultClient);
        Preferences preferences = new Preferences(); // Preferences | 
        String idempotencyKey = "idempotencyKey_example"; // String | 
        String ifMatch = "ifMatch_example"; // String | 
        try {
            ApiResponse<Preferences> response = apiInstance.updatePreferencesWithHttpInfo(preferences, idempotencyKey, ifMatch);
            System.out.println("Status code: " + response.getStatusCode());
            System.out.println("Response headers: " + response.getHeaders());
            System.out.println("Response body: " + response.getData());
        } catch (ApiException e) {
            System.err.println("Exception when calling MeApi#updatePreferences");
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
| **preferences** | [**Preferences**](Preferences.md)|  | |
| **idempotencyKey** | **String**|  | [optional] |
| **ifMatch** | **String**|  | [optional] |

### Return type

ApiResponse<[**Preferences**](Preferences.md)>


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

