# opendustpan.MeApi

All URIs are relative to *http://localhost:8080*

Method | HTTP request | Description
------------- | ------------- | -------------
[**get_preferences**](MeApi.md#get_preferences) | **GET** /api/dustpan/v1/me/preferences | Read the preferences of the caller
[**update_preferences**](MeApi.md#update_preferences) | **PATCH** /api/dustpan/v1/me/preferences | Change the preferences of the caller


# **get_preferences**
> Preferences get_preferences()

Read the preferences of the caller

### Example

* Bearer Authentication (bearerAuth):

```python
import opendustpan
from opendustpan.models.preferences import Preferences
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
    api_instance = opendustpan.MeApi(api_client)

    try:
        # Read the preferences of the caller
        api_response = api_instance.get_preferences()
        print("The response of MeApi->get_preferences:\n")
        pprint(api_response)
    except Exception as e:
        print("Exception when calling MeApi->get_preferences: %s\n" % e)
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
**200** | OK |  * ETag -  <br>  |
**0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to Model list]](../README.md#documentation-for-models) [[Back to README]](../README.md)

# **update_preferences**
> Preferences update_preferences(preferences, idempotency_key=idempotency_key, if_match=if_match)

Change the preferences of the caller

### Example

* Bearer Authentication (bearerAuth):

```python
import opendustpan
from opendustpan.models.preferences import Preferences
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
    api_instance = opendustpan.MeApi(api_client)
    preferences = opendustpan.Preferences() # Preferences | 
    idempotency_key = 'idempotency_key_example' # str |  (optional)
    if_match = 'if_match_example' # str |  (optional)

    try:
        # Change the preferences of the caller
        api_response = api_instance.update_preferences(preferences, idempotency_key=idempotency_key, if_match=if_match)
        print("The response of MeApi->update_preferences:\n")
        pprint(api_response)
    except Exception as e:
        print("Exception when calling MeApi->update_preferences: %s\n" % e)
```



### Parameters


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------
 **preferences** | [**Preferences**](Preferences.md)|  | 
 **idempotency_key** | **str**|  | [optional] 
 **if_match** | **str**|  | [optional] 

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
**200** | OK |  * ETag -  <br>  |
**0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to Model list]](../README.md#documentation-for-models) [[Back to README]](../README.md)

