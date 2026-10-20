# opendustpan.UnitsApi

All URIs are relative to *http://localhost:8080*

Method | HTTP request | Description
------------- | ------------- | -------------
[**get_unit**](UnitsApi.md#get_unit) | **GET** /api/dustpan/v1/namespaces/{namespace}/units/{unit_id} | Read a unit
[**list_units**](UnitsApi.md#list_units) | **GET** /api/dustpan/v1/namespaces/{namespace}/units | List the units of a namespace
[**restore_deleted_unit**](UnitsApi.md#restore_deleted_unit) | **POST** /api/dustpan/v1/namespaces/{namespace}/deleted-units/{unit_id}/restore | Restore a deleted unit


# **get_unit**
> Unit get_unit(namespace, unit_id)

Read a unit

### Example

* Bearer Authentication (bearerAuth):

```python
import opendustpan
from opendustpan.models.unit import Unit
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
    api_instance = opendustpan.UnitsApi(api_client)
    namespace = 'namespace_example' # str | A namespace name, or - for a read across namespaces.
    unit_id = 'unit_id_example' # str | 

    try:
        # Read a unit
        api_response = api_instance.get_unit(namespace, unit_id)
        print("The response of UnitsApi->get_unit:\n")
        pprint(api_response)
    except Exception as e:
        print("Exception when calling UnitsApi->get_unit: %s\n" % e)
```



### Parameters


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------
 **namespace** | **str**| A namespace name, or - for a read across namespaces. | 
 **unit_id** | **str**|  | 

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
**200** | OK |  * ETag -  <br>  |
**0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to Model list]](../README.md#documentation-for-models) [[Back to README]](../README.md)

# **list_units**
> UnitList list_units(namespace, page_size=page_size, page_token=page_token, tool=tool)

List the units of a namespace

### Example

* Bearer Authentication (bearerAuth):

```python
import opendustpan
from opendustpan.models.tool import Tool
from opendustpan.models.unit_list import UnitList
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
    api_instance = opendustpan.UnitsApi(api_client)
    namespace = 'namespace_example' # str | A namespace name, or - for a read across namespaces.
    page_size = 50 # int |  (optional) (default to 50)
    page_token = 'page_token_example' # str |  (optional)
    tool = opendustpan.Tool() # Tool |  (optional)

    try:
        # List the units of a namespace
        api_response = api_instance.list_units(namespace, page_size=page_size, page_token=page_token, tool=tool)
        print("The response of UnitsApi->list_units:\n")
        pprint(api_response)
    except Exception as e:
        print("Exception when calling UnitsApi->list_units: %s\n" % e)
```



### Parameters


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------
 **namespace** | **str**| A namespace name, or - for a read across namespaces. | 
 **page_size** | **int**|  | [optional] [default to 50]
 **page_token** | **str**|  | [optional] 
 **tool** | [**Tool**](.md)|  | [optional] 

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
**200** | OK |  -  |
**0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to Model list]](../README.md#documentation-for-models) [[Back to README]](../README.md)

# **restore_deleted_unit**
> Unit restore_deleted_unit(namespace, unit_id, idempotency_key=idempotency_key)

Restore a deleted unit

### Example

* Bearer Authentication (bearerAuth):

```python
import opendustpan
from opendustpan.models.unit import Unit
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
    api_instance = opendustpan.UnitsApi(api_client)
    namespace = 'namespace_example' # str | A namespace name, or - for a read across namespaces.
    unit_id = 'unit_id_example' # str | 
    idempotency_key = 'idempotency_key_example' # str |  (optional)

    try:
        # Restore a deleted unit
        api_response = api_instance.restore_deleted_unit(namespace, unit_id, idempotency_key=idempotency_key)
        print("The response of UnitsApi->restore_deleted_unit:\n")
        pprint(api_response)
    except Exception as e:
        print("Exception when calling UnitsApi->restore_deleted_unit: %s\n" % e)
```



### Parameters


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------
 **namespace** | **str**| A namespace name, or - for a read across namespaces. | 
 **unit_id** | **str**|  | 
 **idempotency_key** | **str**|  | [optional] 

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
**200** | OK |  -  |
**0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to Model list]](../README.md#documentation-for-models) [[Back to README]](../README.md)

