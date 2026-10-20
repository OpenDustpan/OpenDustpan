# opendustpan.ChangeOrdersApi

All URIs are relative to *http://localhost:8080*

Method | HTTP request | Description
------------- | ------------- | -------------
[**set_change_order_verdict**](ChangeOrdersApi.md#set_change_order_verdict) | **POST** /api/dustpan/v1/namespaces/{namespace}/change-orders/{id}/verdict | Approve or reject a change order


# **set_change_order_verdict**
> ChangeOrder set_change_order_verdict(namespace, id, verdict, idempotency_key=idempotency_key)

Approve or reject a change order

### Example

* Bearer Authentication (bearerAuth):

```python
import opendustpan
from opendustpan.models.change_order import ChangeOrder
from opendustpan.models.verdict import Verdict
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
    api_instance = opendustpan.ChangeOrdersApi(api_client)
    namespace = 'namespace_example' # str | A namespace name, or - for a read across namespaces.
    id = 'id_example' # str | 
    verdict = opendustpan.Verdict() # Verdict | 
    idempotency_key = 'idempotency_key_example' # str |  (optional)

    try:
        # Approve or reject a change order
        api_response = api_instance.set_change_order_verdict(namespace, id, verdict, idempotency_key=idempotency_key)
        print("The response of ChangeOrdersApi->set_change_order_verdict:\n")
        pprint(api_response)
    except Exception as e:
        print("Exception when calling ChangeOrdersApi->set_change_order_verdict: %s\n" % e)
```



### Parameters


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------
 **namespace** | **str**| A namespace name, or - for a read across namespaces. | 
 **id** | **str**|  | 
 **verdict** | [**Verdict**](Verdict.md)|  | 
 **idempotency_key** | **str**|  | [optional] 

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
**200** | OK |  -  |
**0** | An error |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to Model list]](../README.md#documentation-for-models) [[Back to README]](../README.md)

