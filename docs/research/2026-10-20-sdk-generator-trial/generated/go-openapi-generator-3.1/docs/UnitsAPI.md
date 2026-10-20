# \UnitsAPI

All URIs are relative to *http://localhost:8080*

Method | HTTP request | Description
------------- | ------------- | -------------
[**GetUnit**](UnitsAPI.md#GetUnit) | **Get** /api/dustpan/v1/namespaces/{namespace}/units/{unit_id} | Read a unit
[**ListUnits**](UnitsAPI.md#ListUnits) | **Get** /api/dustpan/v1/namespaces/{namespace}/units | List the units of a namespace
[**RestoreDeletedUnit**](UnitsAPI.md#RestoreDeletedUnit) | **Post** /api/dustpan/v1/namespaces/{namespace}/deleted-units/{unit_id}/restore | Restore a deleted unit



## GetUnit

> Unit GetUnit(ctx, namespace, unitId).Execute()

Read a unit

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
	unitId := "unitId_example" // string | 

	configuration := openapiclient.NewConfiguration()
	apiClient := openapiclient.NewAPIClient(configuration)
	resp, r, err := apiClient.UnitsAPI.GetUnit(context.Background(), namespace, unitId).Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error when calling `UnitsAPI.GetUnit``: %v\n", err)
		fmt.Fprintf(os.Stderr, "Full HTTP response: %v\n", r)
	}
	// response from `GetUnit`: Unit
	fmt.Fprintf(os.Stdout, "Response from `UnitsAPI.GetUnit`: %v\n", resp)
}
```

### Path Parameters


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------
**ctx** | **context.Context** | context for authentication, logging, cancellation, deadlines, tracing, etc.
**namespace** | **string** | A namespace name, or - for a read across namespaces. | 
**unitId** | **string** |  | 

### Other Parameters

Other parameters are passed through a pointer to a apiGetUnitRequest struct via the builder pattern


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------



### Return type

[**Unit**](Unit.md)

### Authorization

[bearerAuth](../README.md#bearerAuth)

### HTTP request headers

- **Content-Type**: Not defined
- **Accept**: application/json

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints)
[[Back to Model list]](../README.md#documentation-for-models)
[[Back to README]](../README.md)


## ListUnits

> UnitList ListUnits(ctx, namespace).PageSize(pageSize).PageToken(pageToken).Tool(tool).Execute()

List the units of a namespace

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
	pageSize := int32(56) // int32 |  (optional) (default to 50)
	pageToken := "pageToken_example" // string |  (optional)
	tool := openapiclient.Tool("pulumi") // Tool |  (optional)

	configuration := openapiclient.NewConfiguration()
	apiClient := openapiclient.NewAPIClient(configuration)
	resp, r, err := apiClient.UnitsAPI.ListUnits(context.Background(), namespace).PageSize(pageSize).PageToken(pageToken).Tool(tool).Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error when calling `UnitsAPI.ListUnits``: %v\n", err)
		fmt.Fprintf(os.Stderr, "Full HTTP response: %v\n", r)
	}
	// response from `ListUnits`: UnitList
	fmt.Fprintf(os.Stdout, "Response from `UnitsAPI.ListUnits`: %v\n", resp)
}
```

### Path Parameters


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------
**ctx** | **context.Context** | context for authentication, logging, cancellation, deadlines, tracing, etc.
**namespace** | **string** | A namespace name, or - for a read across namespaces. | 

### Other Parameters

Other parameters are passed through a pointer to a apiListUnitsRequest struct via the builder pattern


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------

 **pageSize** | **int32** |  | [default to 50]
 **pageToken** | **string** |  | 
 **tool** | [**Tool**](Tool.md) |  | 

### Return type

[**UnitList**](UnitList.md)

### Authorization

[bearerAuth](../README.md#bearerAuth)

### HTTP request headers

- **Content-Type**: Not defined
- **Accept**: application/json

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints)
[[Back to Model list]](../README.md#documentation-for-models)
[[Back to README]](../README.md)


## RestoreDeletedUnit

> Unit RestoreDeletedUnit(ctx, namespace, unitId).IdempotencyKey(idempotencyKey).Execute()

Restore a deleted unit

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
	unitId := "unitId_example" // string | 
	idempotencyKey := "idempotencyKey_example" // string |  (optional)

	configuration := openapiclient.NewConfiguration()
	apiClient := openapiclient.NewAPIClient(configuration)
	resp, r, err := apiClient.UnitsAPI.RestoreDeletedUnit(context.Background(), namespace, unitId).IdempotencyKey(idempotencyKey).Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error when calling `UnitsAPI.RestoreDeletedUnit``: %v\n", err)
		fmt.Fprintf(os.Stderr, "Full HTTP response: %v\n", r)
	}
	// response from `RestoreDeletedUnit`: Unit
	fmt.Fprintf(os.Stdout, "Response from `UnitsAPI.RestoreDeletedUnit`: %v\n", resp)
}
```

### Path Parameters


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------
**ctx** | **context.Context** | context for authentication, logging, cancellation, deadlines, tracing, etc.
**namespace** | **string** | A namespace name, or - for a read across namespaces. | 
**unitId** | **string** |  | 

### Other Parameters

Other parameters are passed through a pointer to a apiRestoreDeletedUnitRequest struct via the builder pattern


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------


 **idempotencyKey** | **string** |  | 

### Return type

[**Unit**](Unit.md)

### Authorization

[bearerAuth](../README.md#bearerAuth)

### HTTP request headers

- **Content-Type**: Not defined
- **Accept**: application/json

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints)
[[Back to Model list]](../README.md#documentation-for-models)
[[Back to README]](../README.md)

