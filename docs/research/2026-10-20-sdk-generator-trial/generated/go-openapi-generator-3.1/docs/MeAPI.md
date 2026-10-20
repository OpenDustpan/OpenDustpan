# \MeAPI

All URIs are relative to *http://localhost:8080*

Method | HTTP request | Description
------------- | ------------- | -------------
[**GetPreferences**](MeAPI.md#GetPreferences) | **Get** /api/dustpan/v1/me/preferences | Read the preferences of the caller
[**UpdatePreferences**](MeAPI.md#UpdatePreferences) | **Patch** /api/dustpan/v1/me/preferences | Change the preferences of the caller



## GetPreferences

> Preferences GetPreferences(ctx).Execute()

Read the preferences of the caller

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

	configuration := openapiclient.NewConfiguration()
	apiClient := openapiclient.NewAPIClient(configuration)
	resp, r, err := apiClient.MeAPI.GetPreferences(context.Background()).Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error when calling `MeAPI.GetPreferences``: %v\n", err)
		fmt.Fprintf(os.Stderr, "Full HTTP response: %v\n", r)
	}
	// response from `GetPreferences`: Preferences
	fmt.Fprintf(os.Stdout, "Response from `MeAPI.GetPreferences`: %v\n", resp)
}
```

### Path Parameters

This endpoint does not need any parameter.

### Other Parameters

Other parameters are passed through a pointer to a apiGetPreferencesRequest struct via the builder pattern


### Return type

[**Preferences**](Preferences.md)

### Authorization

[bearerAuth](../README.md#bearerAuth)

### HTTP request headers

- **Content-Type**: Not defined
- **Accept**: application/json

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints)
[[Back to Model list]](../README.md#documentation-for-models)
[[Back to README]](../README.md)


## UpdatePreferences

> Preferences UpdatePreferences(ctx).Preferences(preferences).IdempotencyKey(idempotencyKey).IfMatch(ifMatch).Execute()

Change the preferences of the caller

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
	preferences := *openapiclient.NewPreferences() // Preferences | 
	idempotencyKey := "idempotencyKey_example" // string |  (optional)
	ifMatch := "ifMatch_example" // string |  (optional)

	configuration := openapiclient.NewConfiguration()
	apiClient := openapiclient.NewAPIClient(configuration)
	resp, r, err := apiClient.MeAPI.UpdatePreferences(context.Background()).Preferences(preferences).IdempotencyKey(idempotencyKey).IfMatch(ifMatch).Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error when calling `MeAPI.UpdatePreferences``: %v\n", err)
		fmt.Fprintf(os.Stderr, "Full HTTP response: %v\n", r)
	}
	// response from `UpdatePreferences`: Preferences
	fmt.Fprintf(os.Stdout, "Response from `MeAPI.UpdatePreferences`: %v\n", resp)
}
```

### Path Parameters



### Other Parameters

Other parameters are passed through a pointer to a apiUpdatePreferencesRequest struct via the builder pattern


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------
 **preferences** | [**Preferences**](Preferences.md) |  | 
 **idempotencyKey** | **string** |  | 
 **ifMatch** | **string** |  | 

### Return type

[**Preferences**](Preferences.md)

### Authorization

[bearerAuth](../README.md#bearerAuth)

### HTTP request headers

- **Content-Type**: application/merge-patch+json
- **Accept**: application/json

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints)
[[Back to Model list]](../README.md#documentation-for-models)
[[Back to README]](../README.md)

