# opendustpan.InstallationApi

All URIs are relative to *http://localhost:8080*

Method | HTTP request | Description
------------- | ------------- | -------------
[**get_capabilities**](InstallationApi.md#get_capabilities) | **GET** /api/dustpan/v1/capabilities | Read the API version and the optional modules


# **get_capabilities**
> Capabilities get_capabilities()

Read the API version and the optional modules

### Example


```python
import opendustpan
from opendustpan.models.capabilities import Capabilities
from opendustpan.rest import ApiException
from pprint import pprint

# Defining the host is optional and defaults to http://localhost:8080
# See configuration.py for a list of all supported configuration parameters.
configuration = opendustpan.Configuration(
    host = "http://localhost:8080"
)


# Enter a context with an instance of the API client
with opendustpan.ApiClient(configuration) as api_client:
    # Create an instance of the API class
    api_instance = opendustpan.InstallationApi(api_client)

    try:
        # Read the API version and the optional modules
        api_response = api_instance.get_capabilities()
        print("The response of InstallationApi->get_capabilities:\n")
        pprint(api_response)
    except Exception as e:
        print("Exception when calling InstallationApi->get_capabilities: %s\n" % e)
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
**200** | OK |  -  |
**0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to Model list]](../README.md#documentation-for-models) [[Back to README]](../README.md)

