# \ChangeOrdersAPI

All URIs are relative to *http://localhost:8080*

Method | HTTP request | Description
------------- | ------------- | -------------
[**SetChangeOrderVerdict**](ChangeOrdersAPI.md#SetChangeOrderVerdict) | **Post** /api/dustpan/v1/namespaces/{namespace}/change-orders/{id}/verdict | Approve or reject a change order



## SetChangeOrderVerdict

> ChangeOrder SetChangeOrderVerdict(ctx, namespace, id).Verdict(verdict).IdempotencyKey(idempotencyKey).Execute()

Approve or reject a change order

### Example

```go
package main

import (
	"context"
	"fmt"
	"os"
	openapiclient "github.com/GIT_USER_ID/GIT_REPO_ID"
)

func main() {
	namespace := "namespace_example" // string | A namespace name, or - for a read across namespaces.
	id := "id_example" // string | 
	verdict := *openapiclient.NewVerdict("Decision_example") // Verdict | 
	idempotencyKey := "idempotencyKey_example" // string |  (optional)

	configuration := openapiclient.NewConfiguration()
	apiClient := openapiclient.NewAPIClient(configuration)
	resp, r, err := apiClient.ChangeOrdersAPI.SetChangeOrderVerdict(context.Background(), namespace, id).Verdict(verdict).IdempotencyKey(idempotencyKey).Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error when calling `ChangeOrdersAPI.SetChangeOrderVerdict``: %v\n", err)
		fmt.Fprintf(os.Stderr, "Full HTTP response: %v\n", r)
	}
	// response from `SetChangeOrderVerdict`: ChangeOrder
	fmt.Fprintf(os.Stdout, "Response from `ChangeOrdersAPI.SetChangeOrderVerdict`: %v\n", resp)
}
```

### Path Parameters


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------
**ctx** | **context.Context** | context for authentication, logging, cancellation, deadlines, tracing, etc.
**namespace** | **string** | A namespace name, or - for a read across namespaces. | 
**id** | **string** |  | 

### Other Parameters

Other parameters are passed through a pointer to a apiSetChangeOrderVerdictRequest struct via the builder pattern


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------


 **verdict** | [**Verdict**](Verdict.md) |  | 
 **idempotencyKey** | **string** |  | 

### Return type

[**ChangeOrder**](ChangeOrder.md)

### Authorization

[bearerAuth](../README.md#bearerAuth)

### HTTP request headers

- **Content-Type**: application/json
- **Accept**: application/json

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints)
[[Back to Model list]](../README.md#documentation-for-models)
[[Back to README]](../README.md)

