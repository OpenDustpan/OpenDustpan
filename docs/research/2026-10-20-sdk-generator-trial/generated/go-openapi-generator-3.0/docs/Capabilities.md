# Capabilities

## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**ApiVersion** | **string** |  | 
**Modules** | Pointer to [**[]Module**](Module.md) |  | [optional] 

## Methods

### NewCapabilities

`func NewCapabilities(apiVersion string, ) *Capabilities`

NewCapabilities instantiates a new Capabilities object
This constructor will assign default values to properties that have it defined,
and makes sure properties required by API are set, but the set of arguments
will change when the set of required properties is changed

### NewCapabilitiesWithDefaults

`func NewCapabilitiesWithDefaults() *Capabilities`

NewCapabilitiesWithDefaults instantiates a new Capabilities object
This constructor will only assign default values to properties that have it defined,
but it doesn't guarantee that properties required by API are set

### GetApiVersion

`func (o *Capabilities) GetApiVersion() string`

GetApiVersion returns the ApiVersion field if non-nil, zero value otherwise.

### GetApiVersionOk

`func (o *Capabilities) GetApiVersionOk() (*string, bool)`

GetApiVersionOk returns a tuple with the ApiVersion field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetApiVersion

`func (o *Capabilities) SetApiVersion(v string)`

SetApiVersion sets ApiVersion field to given value.


### GetModules

`func (o *Capabilities) GetModules() []Module`

GetModules returns the Modules field if non-nil, zero value otherwise.

### GetModulesOk

`func (o *Capabilities) GetModulesOk() (*[]Module, bool)`

GetModulesOk returns a tuple with the Modules field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetModules

`func (o *Capabilities) SetModules(v []Module)`

SetModules sets Modules field to given value.

### HasModules

`func (o *Capabilities) HasModules() bool`

HasModules returns a boolean if a field has been set.


[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


