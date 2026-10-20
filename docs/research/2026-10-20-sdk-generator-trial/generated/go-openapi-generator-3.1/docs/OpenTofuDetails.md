# OpenTofuDetails

## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**TerraformVersion** | Pointer to **NullableString** |  | [optional] 
**Tool** | **string** |  | 
**Workspace** | **string** |  | 

## Methods

### NewOpenTofuDetails

`func NewOpenTofuDetails(tool string, workspace string, ) *OpenTofuDetails`

NewOpenTofuDetails instantiates a new OpenTofuDetails object
This constructor will assign default values to properties that have it defined,
and makes sure properties required by API are set, but the set of arguments
will change when the set of required properties is changed

### NewOpenTofuDetailsWithDefaults

`func NewOpenTofuDetailsWithDefaults() *OpenTofuDetails`

NewOpenTofuDetailsWithDefaults instantiates a new OpenTofuDetails object
This constructor will only assign default values to properties that have it defined,
but it doesn't guarantee that properties required by API are set

### GetTerraformVersion

`func (o *OpenTofuDetails) GetTerraformVersion() string`

GetTerraformVersion returns the TerraformVersion field if non-nil, zero value otherwise.

### GetTerraformVersionOk

`func (o *OpenTofuDetails) GetTerraformVersionOk() (*string, bool)`

GetTerraformVersionOk returns a tuple with the TerraformVersion field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetTerraformVersion

`func (o *OpenTofuDetails) SetTerraformVersion(v string)`

SetTerraformVersion sets TerraformVersion field to given value.

### HasTerraformVersion

`func (o *OpenTofuDetails) HasTerraformVersion() bool`

HasTerraformVersion returns a boolean if a field has been set.

### SetTerraformVersionNil

`func (o *OpenTofuDetails) SetTerraformVersionNil(b bool)`

 SetTerraformVersionNil sets the value for TerraformVersion to be an explicit nil

### UnsetTerraformVersion
`func (o *OpenTofuDetails) UnsetTerraformVersion()`

UnsetTerraformVersion ensures that no value is present for TerraformVersion, not even an explicit nil
### GetTool

`func (o *OpenTofuDetails) GetTool() string`

GetTool returns the Tool field if non-nil, zero value otherwise.

### GetToolOk

`func (o *OpenTofuDetails) GetToolOk() (*string, bool)`

GetToolOk returns a tuple with the Tool field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetTool

`func (o *OpenTofuDetails) SetTool(v string)`

SetTool sets Tool field to given value.


### GetWorkspace

`func (o *OpenTofuDetails) GetWorkspace() string`

GetWorkspace returns the Workspace field if non-nil, zero value otherwise.

### GetWorkspaceOk

`func (o *OpenTofuDetails) GetWorkspaceOk() (*string, bool)`

GetWorkspaceOk returns a tuple with the Workspace field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetWorkspace

`func (o *OpenTofuDetails) SetWorkspace(v string)`

SetWorkspace sets Workspace field to given value.



[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


