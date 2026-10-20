# UnitDetails

## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**Project** | **string** |  | 
**Stack** | **string** |  | 
**Tool** | **string** |  | 
**TerraformVersion** | Pointer to **NullableString** |  | [optional] 
**Workspace** | **string** |  | 

## Methods

### NewUnitDetails

`func NewUnitDetails(project string, stack string, tool string, workspace string, ) *UnitDetails`

NewUnitDetails instantiates a new UnitDetails object
This constructor will assign default values to properties that have it defined,
and makes sure properties required by API are set, but the set of arguments
will change when the set of required properties is changed

### NewUnitDetailsWithDefaults

`func NewUnitDetailsWithDefaults() *UnitDetails`

NewUnitDetailsWithDefaults instantiates a new UnitDetails object
This constructor will only assign default values to properties that have it defined,
but it doesn't guarantee that properties required by API are set

### GetProject

`func (o *UnitDetails) GetProject() string`

GetProject returns the Project field if non-nil, zero value otherwise.

### GetProjectOk

`func (o *UnitDetails) GetProjectOk() (*string, bool)`

GetProjectOk returns a tuple with the Project field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetProject

`func (o *UnitDetails) SetProject(v string)`

SetProject sets Project field to given value.


### GetStack

`func (o *UnitDetails) GetStack() string`

GetStack returns the Stack field if non-nil, zero value otherwise.

### GetStackOk

`func (o *UnitDetails) GetStackOk() (*string, bool)`

GetStackOk returns a tuple with the Stack field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetStack

`func (o *UnitDetails) SetStack(v string)`

SetStack sets Stack field to given value.


### GetTool

`func (o *UnitDetails) GetTool() string`

GetTool returns the Tool field if non-nil, zero value otherwise.

### GetToolOk

`func (o *UnitDetails) GetToolOk() (*string, bool)`

GetToolOk returns a tuple with the Tool field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetTool

`func (o *UnitDetails) SetTool(v string)`

SetTool sets Tool field to given value.


### GetTerraformVersion

`func (o *UnitDetails) GetTerraformVersion() string`

GetTerraformVersion returns the TerraformVersion field if non-nil, zero value otherwise.

### GetTerraformVersionOk

`func (o *UnitDetails) GetTerraformVersionOk() (*string, bool)`

GetTerraformVersionOk returns a tuple with the TerraformVersion field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetTerraformVersion

`func (o *UnitDetails) SetTerraformVersion(v string)`

SetTerraformVersion sets TerraformVersion field to given value.

### HasTerraformVersion

`func (o *UnitDetails) HasTerraformVersion() bool`

HasTerraformVersion returns a boolean if a field has been set.

### SetTerraformVersionNil

`func (o *UnitDetails) SetTerraformVersionNil(b bool)`

 SetTerraformVersionNil sets the value for TerraformVersion to be an explicit nil

### UnsetTerraformVersion
`func (o *UnitDetails) UnsetTerraformVersion()`

UnsetTerraformVersion ensures that no value is present for TerraformVersion, not even an explicit nil
### GetWorkspace

`func (o *UnitDetails) GetWorkspace() string`

GetWorkspace returns the Workspace field if non-nil, zero value otherwise.

### GetWorkspaceOk

`func (o *UnitDetails) GetWorkspaceOk() (*string, bool)`

GetWorkspaceOk returns a tuple with the Workspace field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetWorkspace

`func (o *UnitDetails) SetWorkspace(v string)`

SetWorkspace sets Workspace field to given value.



[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


