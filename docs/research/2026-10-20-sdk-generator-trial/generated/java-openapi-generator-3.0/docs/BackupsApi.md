# BackupsApi

All URIs are relative to *http://localhost:8080*

| Method | HTTP request | Description |
|------------- | ------------- | -------------|
| [**checkBackup**](BackupsApi.md#checkBackup) | **POST** /api/dustpan/v1/backups/{id}/check | Check a backup |
| [**checkBackupWithHttpInfo**](BackupsApi.md#checkBackupWithHttpInfo) | **POST** /api/dustpan/v1/backups/{id}/check | Check a backup |
| [**createBackup**](BackupsApi.md#createBackup) | **POST** /api/dustpan/v1/backups | Start a backup |
| [**createBackupWithHttpInfo**](BackupsApi.md#createBackupWithHttpInfo) | **POST** /api/dustpan/v1/backups | Start a backup |
| [**deleteBackup**](BackupsApi.md#deleteBackup) | **DELETE** /api/dustpan/v1/backups/{id} | Delete a backup |
| [**deleteBackupWithHttpInfo**](BackupsApi.md#deleteBackupWithHttpInfo) | **DELETE** /api/dustpan/v1/backups/{id} | Delete a backup |
| [**getBackup**](BackupsApi.md#getBackup) | **GET** /api/dustpan/v1/backups/{id} | Read a backup |
| [**getBackupWithHttpInfo**](BackupsApi.md#getBackupWithHttpInfo) | **GET** /api/dustpan/v1/backups/{id} | Read a backup |
| [**listBackups**](BackupsApi.md#listBackups) | **GET** /api/dustpan/v1/backups | List backups |
| [**listBackupsWithHttpInfo**](BackupsApi.md#listBackupsWithHttpInfo) | **GET** /api/dustpan/v1/backups | List backups |



## checkBackup

> BackupCheck checkBackup(id, idempotencyKey)

Check a backup

### Example

```java
// Import classes:
import org.opendustpan.client.ApiClient;
import org.opendustpan.client.ApiException;
import org.opendustpan.client.Configuration;
import org.opendustpan.client.auth.*;
import org.opendustpan.client.models.*;
import org.opendustpan.client.api.BackupsApi;

public class Example {
    public static void main(String[] args) {
        ApiClient defaultClient = Configuration.getDefaultApiClient();
        defaultClient.setBasePath("http://localhost:8080");
        
        // Configure HTTP bearer authorization: bearerAuth
        HttpBearerAuth bearerAuth = (HttpBearerAuth) defaultClient.getAuthentication("bearerAuth");
        bearerAuth.setBearerToken("BEARER TOKEN");

        BackupsApi apiInstance = new BackupsApi(defaultClient);
        String id = "id_example"; // String | 
        String idempotencyKey = "idempotencyKey_example"; // String | 
        try {
            BackupCheck result = apiInstance.checkBackup(id, idempotencyKey);
            System.out.println(result);
        } catch (ApiException e) {
            System.err.println("Exception when calling BackupsApi#checkBackup");
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
| **id** | **String**|  | |
| **idempotencyKey** | **String**|  | [optional] |

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

## checkBackupWithHttpInfo

> ApiResponse<BackupCheck> checkBackupWithHttpInfo(id, idempotencyKey)

Check a backup

### Example

```java
// Import classes:
import org.opendustpan.client.ApiClient;
import org.opendustpan.client.ApiException;
import org.opendustpan.client.ApiResponse;
import org.opendustpan.client.Configuration;
import org.opendustpan.client.auth.*;
import org.opendustpan.client.models.*;
import org.opendustpan.client.api.BackupsApi;

public class Example {
    public static void main(String[] args) {
        ApiClient defaultClient = Configuration.getDefaultApiClient();
        defaultClient.setBasePath("http://localhost:8080");
        
        // Configure HTTP bearer authorization: bearerAuth
        HttpBearerAuth bearerAuth = (HttpBearerAuth) defaultClient.getAuthentication("bearerAuth");
        bearerAuth.setBearerToken("BEARER TOKEN");

        BackupsApi apiInstance = new BackupsApi(defaultClient);
        String id = "id_example"; // String | 
        String idempotencyKey = "idempotencyKey_example"; // String | 
        try {
            ApiResponse<BackupCheck> response = apiInstance.checkBackupWithHttpInfo(id, idempotencyKey);
            System.out.println("Status code: " + response.getStatusCode());
            System.out.println("Response headers: " + response.getHeaders());
            System.out.println("Response body: " + response.getData());
        } catch (ApiException e) {
            System.err.println("Exception when calling BackupsApi#checkBackup");
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
| **id** | **String**|  | |
| **idempotencyKey** | **String**|  | [optional] |

### Return type

ApiResponse<[**BackupCheck**](BackupCheck.md)>


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


## createBackup

> Backup createBackup(idempotencyKey, backupCreate)

Start a backup

### Example

```java
// Import classes:
import org.opendustpan.client.ApiClient;
import org.opendustpan.client.ApiException;
import org.opendustpan.client.Configuration;
import org.opendustpan.client.auth.*;
import org.opendustpan.client.models.*;
import org.opendustpan.client.api.BackupsApi;

public class Example {
    public static void main(String[] args) {
        ApiClient defaultClient = Configuration.getDefaultApiClient();
        defaultClient.setBasePath("http://localhost:8080");
        
        // Configure HTTP bearer authorization: bearerAuth
        HttpBearerAuth bearerAuth = (HttpBearerAuth) defaultClient.getAuthentication("bearerAuth");
        bearerAuth.setBearerToken("BEARER TOKEN");

        BackupsApi apiInstance = new BackupsApi(defaultClient);
        String idempotencyKey = "idempotencyKey_example"; // String | 
        BackupCreate backupCreate = new BackupCreate(); // BackupCreate | 
        try {
            Backup result = apiInstance.createBackup(idempotencyKey, backupCreate);
            System.out.println(result);
        } catch (ApiException e) {
            System.err.println("Exception when calling BackupsApi#createBackup");
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
| **idempotencyKey** | **String**|  | [optional] |
| **backupCreate** | [**BackupCreate**](BackupCreate.md)|  | [optional] |

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

## createBackupWithHttpInfo

> ApiResponse<Backup> createBackupWithHttpInfo(idempotencyKey, backupCreate)

Start a backup

### Example

```java
// Import classes:
import org.opendustpan.client.ApiClient;
import org.opendustpan.client.ApiException;
import org.opendustpan.client.ApiResponse;
import org.opendustpan.client.Configuration;
import org.opendustpan.client.auth.*;
import org.opendustpan.client.models.*;
import org.opendustpan.client.api.BackupsApi;

public class Example {
    public static void main(String[] args) {
        ApiClient defaultClient = Configuration.getDefaultApiClient();
        defaultClient.setBasePath("http://localhost:8080");
        
        // Configure HTTP bearer authorization: bearerAuth
        HttpBearerAuth bearerAuth = (HttpBearerAuth) defaultClient.getAuthentication("bearerAuth");
        bearerAuth.setBearerToken("BEARER TOKEN");

        BackupsApi apiInstance = new BackupsApi(defaultClient);
        String idempotencyKey = "idempotencyKey_example"; // String | 
        BackupCreate backupCreate = new BackupCreate(); // BackupCreate | 
        try {
            ApiResponse<Backup> response = apiInstance.createBackupWithHttpInfo(idempotencyKey, backupCreate);
            System.out.println("Status code: " + response.getStatusCode());
            System.out.println("Response headers: " + response.getHeaders());
            System.out.println("Response body: " + response.getData());
        } catch (ApiException e) {
            System.err.println("Exception when calling BackupsApi#createBackup");
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
| **idempotencyKey** | **String**|  | [optional] |
| **backupCreate** | [**BackupCreate**](BackupCreate.md)|  | [optional] |

### Return type

ApiResponse<[**Backup**](Backup.md)>


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


## deleteBackup

> void deleteBackup(id, ifMatch, idempotencyKey)

Delete a backup

### Example

```java
// Import classes:
import org.opendustpan.client.ApiClient;
import org.opendustpan.client.ApiException;
import org.opendustpan.client.Configuration;
import org.opendustpan.client.auth.*;
import org.opendustpan.client.models.*;
import org.opendustpan.client.api.BackupsApi;

public class Example {
    public static void main(String[] args) {
        ApiClient defaultClient = Configuration.getDefaultApiClient();
        defaultClient.setBasePath("http://localhost:8080");
        
        // Configure HTTP bearer authorization: bearerAuth
        HttpBearerAuth bearerAuth = (HttpBearerAuth) defaultClient.getAuthentication("bearerAuth");
        bearerAuth.setBearerToken("BEARER TOKEN");

        BackupsApi apiInstance = new BackupsApi(defaultClient);
        String id = "id_example"; // String | 
        String ifMatch = "ifMatch_example"; // String | 
        String idempotencyKey = "idempotencyKey_example"; // String | 
        try {
            apiInstance.deleteBackup(id, ifMatch, idempotencyKey);
        } catch (ApiException e) {
            System.err.println("Exception when calling BackupsApi#deleteBackup");
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
| **id** | **String**|  | |
| **ifMatch** | **String**|  | |
| **idempotencyKey** | **String**|  | [optional] |

### Return type


null (empty response body)

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

## deleteBackupWithHttpInfo

> ApiResponse<Void> deleteBackupWithHttpInfo(id, ifMatch, idempotencyKey)

Delete a backup

### Example

```java
// Import classes:
import org.opendustpan.client.ApiClient;
import org.opendustpan.client.ApiException;
import org.opendustpan.client.ApiResponse;
import org.opendustpan.client.Configuration;
import org.opendustpan.client.auth.*;
import org.opendustpan.client.models.*;
import org.opendustpan.client.api.BackupsApi;

public class Example {
    public static void main(String[] args) {
        ApiClient defaultClient = Configuration.getDefaultApiClient();
        defaultClient.setBasePath("http://localhost:8080");
        
        // Configure HTTP bearer authorization: bearerAuth
        HttpBearerAuth bearerAuth = (HttpBearerAuth) defaultClient.getAuthentication("bearerAuth");
        bearerAuth.setBearerToken("BEARER TOKEN");

        BackupsApi apiInstance = new BackupsApi(defaultClient);
        String id = "id_example"; // String | 
        String ifMatch = "ifMatch_example"; // String | 
        String idempotencyKey = "idempotencyKey_example"; // String | 
        try {
            ApiResponse<Void> response = apiInstance.deleteBackupWithHttpInfo(id, ifMatch, idempotencyKey);
            System.out.println("Status code: " + response.getStatusCode());
            System.out.println("Response headers: " + response.getHeaders());
        } catch (ApiException e) {
            System.err.println("Exception when calling BackupsApi#deleteBackup");
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
| **id** | **String**|  | |
| **ifMatch** | **String**|  | |
| **idempotencyKey** | **String**|  | [optional] |

### Return type


ApiResponse<Void>

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


## getBackup

> Backup getBackup(id)

Read a backup

### Example

```java
// Import classes:
import org.opendustpan.client.ApiClient;
import org.opendustpan.client.ApiException;
import org.opendustpan.client.Configuration;
import org.opendustpan.client.auth.*;
import org.opendustpan.client.models.*;
import org.opendustpan.client.api.BackupsApi;

public class Example {
    public static void main(String[] args) {
        ApiClient defaultClient = Configuration.getDefaultApiClient();
        defaultClient.setBasePath("http://localhost:8080");
        
        // Configure HTTP bearer authorization: bearerAuth
        HttpBearerAuth bearerAuth = (HttpBearerAuth) defaultClient.getAuthentication("bearerAuth");
        bearerAuth.setBearerToken("BEARER TOKEN");

        BackupsApi apiInstance = new BackupsApi(defaultClient);
        String id = "id_example"; // String | 
        try {
            Backup result = apiInstance.getBackup(id);
            System.out.println(result);
        } catch (ApiException e) {
            System.err.println("Exception when calling BackupsApi#getBackup");
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
| **id** | **String**|  | |

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

## getBackupWithHttpInfo

> ApiResponse<Backup> getBackupWithHttpInfo(id)

Read a backup

### Example

```java
// Import classes:
import org.opendustpan.client.ApiClient;
import org.opendustpan.client.ApiException;
import org.opendustpan.client.ApiResponse;
import org.opendustpan.client.Configuration;
import org.opendustpan.client.auth.*;
import org.opendustpan.client.models.*;
import org.opendustpan.client.api.BackupsApi;

public class Example {
    public static void main(String[] args) {
        ApiClient defaultClient = Configuration.getDefaultApiClient();
        defaultClient.setBasePath("http://localhost:8080");
        
        // Configure HTTP bearer authorization: bearerAuth
        HttpBearerAuth bearerAuth = (HttpBearerAuth) defaultClient.getAuthentication("bearerAuth");
        bearerAuth.setBearerToken("BEARER TOKEN");

        BackupsApi apiInstance = new BackupsApi(defaultClient);
        String id = "id_example"; // String | 
        try {
            ApiResponse<Backup> response = apiInstance.getBackupWithHttpInfo(id);
            System.out.println("Status code: " + response.getStatusCode());
            System.out.println("Response headers: " + response.getHeaders());
            System.out.println("Response body: " + response.getData());
        } catch (ApiException e) {
            System.err.println("Exception when calling BackupsApi#getBackup");
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
| **id** | **String**|  | |

### Return type

ApiResponse<[**Backup**](Backup.md)>


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


## listBackups

> BackupList listBackups(pageSize, pageToken)

List backups

### Example

```java
// Import classes:
import org.opendustpan.client.ApiClient;
import org.opendustpan.client.ApiException;
import org.opendustpan.client.Configuration;
import org.opendustpan.client.auth.*;
import org.opendustpan.client.models.*;
import org.opendustpan.client.api.BackupsApi;

public class Example {
    public static void main(String[] args) {
        ApiClient defaultClient = Configuration.getDefaultApiClient();
        defaultClient.setBasePath("http://localhost:8080");
        
        // Configure HTTP bearer authorization: bearerAuth
        HttpBearerAuth bearerAuth = (HttpBearerAuth) defaultClient.getAuthentication("bearerAuth");
        bearerAuth.setBearerToken("BEARER TOKEN");

        BackupsApi apiInstance = new BackupsApi(defaultClient);
        Integer pageSize = 50; // Integer | 
        String pageToken = "pageToken_example"; // String | 
        try {
            BackupList result = apiInstance.listBackups(pageSize, pageToken);
            System.out.println(result);
        } catch (ApiException e) {
            System.err.println("Exception when calling BackupsApi#listBackups");
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
| **pageSize** | **Integer**|  | [optional] [default to 50] |
| **pageToken** | **String**|  | [optional] |

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

## listBackupsWithHttpInfo

> ApiResponse<BackupList> listBackupsWithHttpInfo(pageSize, pageToken)

List backups

### Example

```java
// Import classes:
import org.opendustpan.client.ApiClient;
import org.opendustpan.client.ApiException;
import org.opendustpan.client.ApiResponse;
import org.opendustpan.client.Configuration;
import org.opendustpan.client.auth.*;
import org.opendustpan.client.models.*;
import org.opendustpan.client.api.BackupsApi;

public class Example {
    public static void main(String[] args) {
        ApiClient defaultClient = Configuration.getDefaultApiClient();
        defaultClient.setBasePath("http://localhost:8080");
        
        // Configure HTTP bearer authorization: bearerAuth
        HttpBearerAuth bearerAuth = (HttpBearerAuth) defaultClient.getAuthentication("bearerAuth");
        bearerAuth.setBearerToken("BEARER TOKEN");

        BackupsApi apiInstance = new BackupsApi(defaultClient);
        Integer pageSize = 50; // Integer | 
        String pageToken = "pageToken_example"; // String | 
        try {
            ApiResponse<BackupList> response = apiInstance.listBackupsWithHttpInfo(pageSize, pageToken);
            System.out.println("Status code: " + response.getStatusCode());
            System.out.println("Response headers: " + response.getHeaders());
            System.out.println("Response body: " + response.getData());
        } catch (ApiException e) {
            System.err.println("Exception when calling BackupsApi#listBackups");
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
| **pageSize** | **Integer**|  | [optional] [default to 50] |
| **pageToken** | **String**|  | [optional] |

### Return type

ApiResponse<[**BackupList**](BackupList.md)>


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

