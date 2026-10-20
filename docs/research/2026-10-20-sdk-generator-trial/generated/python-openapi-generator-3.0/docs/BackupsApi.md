# opendustpan.BackupsApi

All URIs are relative to *http://localhost:8080*

Method | HTTP request | Description
------------- | ------------- | -------------
[**check_backup**](BackupsApi.md#check_backup) | **POST** /api/dustpan/v1/backups/{id}/check | Check a backup
[**create_backup**](BackupsApi.md#create_backup) | **POST** /api/dustpan/v1/backups | Start a backup
[**delete_backup**](BackupsApi.md#delete_backup) | **DELETE** /api/dustpan/v1/backups/{id} | Delete a backup
[**get_backup**](BackupsApi.md#get_backup) | **GET** /api/dustpan/v1/backups/{id} | Read a backup
[**list_backups**](BackupsApi.md#list_backups) | **GET** /api/dustpan/v1/backups | List backups


# **check_backup**
> BackupCheck check_backup(id, idempotency_key=idempotency_key)

Check a backup

### Example

* Bearer Authentication (bearerAuth):

```python
import opendustpan
from opendustpan.models.backup_check import BackupCheck
from opendustpan.rest import ApiException
from pprint import pprint

# Defining the host is optional and defaults to http://localhost:8080
# See configuration.py for a list of all supported configuration parameters.
configuration = opendustpan.Configuration(
    host = "http://localhost:8080"
)

# The client must configure the authentication and authorization parameters
# in accordance with the API server security policy.
# Examples for each auth method are provided below, use the example that
# satisfies your auth use case.

# Configure Bearer authorization: bearerAuth
configuration = opendustpan.Configuration(
    access_token = os.environ["BEARER_TOKEN"]
)

# Enter a context with an instance of the API client
with opendustpan.ApiClient(configuration) as api_client:
    # Create an instance of the API class
    api_instance = opendustpan.BackupsApi(api_client)
    id = 'id_example' # str | 
    idempotency_key = 'idempotency_key_example' # str |  (optional)

    try:
        # Check a backup
        api_response = api_instance.check_backup(id, idempotency_key=idempotency_key)
        print("The response of BackupsApi->check_backup:\n")
        pprint(api_response)
    except Exception as e:
        print("Exception when calling BackupsApi->check_backup: %s\n" % e)
```



### Parameters


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------
 **id** | **str**|  | 
 **idempotency_key** | **str**|  | [optional] 

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
**200** | OK |  -  |
**0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to Model list]](../README.md#documentation-for-models) [[Back to README]](../README.md)

# **create_backup**
> Backup create_backup(idempotency_key=idempotency_key, backup_create=backup_create)

Start a backup

### Example

* Bearer Authentication (bearerAuth):

```python
import opendustpan
from opendustpan.models.backup import Backup
from opendustpan.models.backup_create import BackupCreate
from opendustpan.rest import ApiException
from pprint import pprint

# Defining the host is optional and defaults to http://localhost:8080
# See configuration.py for a list of all supported configuration parameters.
configuration = opendustpan.Configuration(
    host = "http://localhost:8080"
)

# The client must configure the authentication and authorization parameters
# in accordance with the API server security policy.
# Examples for each auth method are provided below, use the example that
# satisfies your auth use case.

# Configure Bearer authorization: bearerAuth
configuration = opendustpan.Configuration(
    access_token = os.environ["BEARER_TOKEN"]
)

# Enter a context with an instance of the API client
with opendustpan.ApiClient(configuration) as api_client:
    # Create an instance of the API class
    api_instance = opendustpan.BackupsApi(api_client)
    idempotency_key = 'idempotency_key_example' # str |  (optional)
    backup_create = opendustpan.BackupCreate() # BackupCreate |  (optional)

    try:
        # Start a backup
        api_response = api_instance.create_backup(idempotency_key=idempotency_key, backup_create=backup_create)
        print("The response of BackupsApi->create_backup:\n")
        pprint(api_response)
    except Exception as e:
        print("Exception when calling BackupsApi->create_backup: %s\n" % e)
```



### Parameters


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------
 **idempotency_key** | **str**|  | [optional] 
 **backup_create** | [**BackupCreate**](BackupCreate.md)|  | [optional] 

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
**202** | Accepted |  * ETag -  <br>  |
**0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to Model list]](../README.md#documentation-for-models) [[Back to README]](../README.md)

# **delete_backup**
> delete_backup(id, if_match, idempotency_key=idempotency_key)

Delete a backup

### Example

* Bearer Authentication (bearerAuth):

```python
import opendustpan
from opendustpan.rest import ApiException
from pprint import pprint

# Defining the host is optional and defaults to http://localhost:8080
# See configuration.py for a list of all supported configuration parameters.
configuration = opendustpan.Configuration(
    host = "http://localhost:8080"
)

# The client must configure the authentication and authorization parameters
# in accordance with the API server security policy.
# Examples for each auth method are provided below, use the example that
# satisfies your auth use case.

# Configure Bearer authorization: bearerAuth
configuration = opendustpan.Configuration(
    access_token = os.environ["BEARER_TOKEN"]
)

# Enter a context with an instance of the API client
with opendustpan.ApiClient(configuration) as api_client:
    # Create an instance of the API class
    api_instance = opendustpan.BackupsApi(api_client)
    id = 'id_example' # str | 
    if_match = 'if_match_example' # str | 
    idempotency_key = 'idempotency_key_example' # str |  (optional)

    try:
        # Delete a backup
        api_instance.delete_backup(id, if_match, idempotency_key=idempotency_key)
    except Exception as e:
        print("Exception when calling BackupsApi->delete_backup: %s\n" % e)
```



### Parameters


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------
 **id** | **str**|  | 
 **if_match** | **str**|  | 
 **idempotency_key** | **str**|  | [optional] 

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
**204** | Deleted |  -  |
**0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to Model list]](../README.md#documentation-for-models) [[Back to README]](../README.md)

# **get_backup**
> Backup get_backup(id)

Read a backup

### Example

* Bearer Authentication (bearerAuth):

```python
import opendustpan
from opendustpan.models.backup import Backup
from opendustpan.rest import ApiException
from pprint import pprint

# Defining the host is optional and defaults to http://localhost:8080
# See configuration.py for a list of all supported configuration parameters.
configuration = opendustpan.Configuration(
    host = "http://localhost:8080"
)

# The client must configure the authentication and authorization parameters
# in accordance with the API server security policy.
# Examples for each auth method are provided below, use the example that
# satisfies your auth use case.

# Configure Bearer authorization: bearerAuth
configuration = opendustpan.Configuration(
    access_token = os.environ["BEARER_TOKEN"]
)

# Enter a context with an instance of the API client
with opendustpan.ApiClient(configuration) as api_client:
    # Create an instance of the API class
    api_instance = opendustpan.BackupsApi(api_client)
    id = 'id_example' # str | 

    try:
        # Read a backup
        api_response = api_instance.get_backup(id)
        print("The response of BackupsApi->get_backup:\n")
        pprint(api_response)
    except Exception as e:
        print("Exception when calling BackupsApi->get_backup: %s\n" % e)
```



### Parameters


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------
 **id** | **str**|  | 

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
**200** | OK |  * ETag -  <br>  |
**0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to Model list]](../README.md#documentation-for-models) [[Back to README]](../README.md)

# **list_backups**
> BackupList list_backups(page_size=page_size, page_token=page_token)

List backups

### Example

* Bearer Authentication (bearerAuth):

```python
import opendustpan
from opendustpan.models.backup_list import BackupList
from opendustpan.rest import ApiException
from pprint import pprint

# Defining the host is optional and defaults to http://localhost:8080
# See configuration.py for a list of all supported configuration parameters.
configuration = opendustpan.Configuration(
    host = "http://localhost:8080"
)

# The client must configure the authentication and authorization parameters
# in accordance with the API server security policy.
# Examples for each auth method are provided below, use the example that
# satisfies your auth use case.

# Configure Bearer authorization: bearerAuth
configuration = opendustpan.Configuration(
    access_token = os.environ["BEARER_TOKEN"]
)

# Enter a context with an instance of the API client
with opendustpan.ApiClient(configuration) as api_client:
    # Create an instance of the API class
    api_instance = opendustpan.BackupsApi(api_client)
    page_size = 50 # int |  (optional) (default to 50)
    page_token = 'page_token_example' # str |  (optional)

    try:
        # List backups
        api_response = api_instance.list_backups(page_size=page_size, page_token=page_token)
        print("The response of BackupsApi->list_backups:\n")
        pprint(api_response)
    except Exception as e:
        print("Exception when calling BackupsApi->list_backups: %s\n" % e)
```



### Parameters


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------
 **page_size** | **int**|  | [optional] [default to 50]
 **page_token** | **str**|  | [optional] 

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
**200** | OK |  -  |
**0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to Model list]](../README.md#documentation-for-models) [[Back to README]](../README.md)

