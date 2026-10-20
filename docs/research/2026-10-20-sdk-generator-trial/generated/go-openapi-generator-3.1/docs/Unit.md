# Unit

## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**Details** | [**UnitDetails**](UnitDetails.md) |  | 
**Labels** | Pointer to **map[string]string** |  | [optional] 
**LastChange** | **time.Time** |  | 
**Lock** | Pointer to [**NullableLock**](Lock.md) |  | [optional] 
**Name** | **string** |  | 
**Namespace** | **string** |  | 
**ResourceCount** | Pointer to **NullableInt64** |  | [optional] 
**Tool** | [**Tool**](Tool.md) |  | 
**UnitId** | **string** |  | 

## Methods

### NewUnit

`func NewUnit(details UnitDetails, lastChange time.Time, name string, namespace string, tool Tool, unitId string, ) *Unit`

NewUnit instantiates a new Unit object
This constructor will assign default values to properties that have it defined,
and makes sure properties required by API are set, but the set of arguments
will change when the set of required properties is changed

### NewUnitWithDefaults

`func NewUnitWithDefaults() *Unit`

NewUnitWithDefaults instantiates a new Unit object
This constructor will only assign default values to properties that have it defined,
but it doesn't guarantee that properties required by API are set

### GetDetails

`func (o *Unit) GetDetails() UnitDetails`

GetDetails returns the Details field if non-nil, zero value otherwise.

### GetDetailsOk

`func (o *Unit) GetDetailsOk() (*UnitDetails, bool)`

GetDetailsOk returns a tuple with the Details field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetDetails

`func (o *Unit) SetDetails(v UnitDetails)`

SetDetails sets Details field to given value.


### GetLabels

`func (o *Unit) GetLabels() map[string]string`

GetLabels returns the Labels field if non-nil, zero value otherwise.

### GetLabelsOk

`func (o *Unit) GetLabelsOk() (*map[string]string, bool)`

GetLabelsOk returns a tuple with the Labels field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetLabels

`func (o *Unit) SetLabels(v map[string]string)`

SetLabels sets Labels field to given value.

### HasLabels

`func (o *Unit) HasLabels() bool`

HasLabels returns a boolean if a field has been set.

### GetLastChange

`func (o *Unit) GetLastChange() time.Time`

GetLastChange returns the LastChange field if non-nil, zero value otherwise.

### GetLastChangeOk

`func (o *Unit) GetLastChangeOk() (*time.Time, bool)`

GetLastChangeOk returns a tuple with the LastChange field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetLastChange

`func (o *Unit) SetLastChange(v time.Time)`

SetLastChange sets LastChange field to given value.


### GetLock

`func (o *Unit) GetLock() Lock`

GetLock returns the Lock field if non-nil, zero value otherwise.

### GetLockOk

`func (o *Unit) GetLockOk() (*Lock, bool)`

GetLockOk returns a tuple with the Lock field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetLock

`func (o *Unit) SetLock(v Lock)`

SetLock sets Lock field to given value.

### HasLock

`func (o *Unit) HasLock() bool`

HasLock returns a boolean if a field has been set.

### SetLockNil

`func (o *Unit) SetLockNil(b bool)`

 SetLockNil sets the value for Lock to be an explicit nil

### UnsetLock
`func (o *Unit) UnsetLock()`

UnsetLock ensures that no value is present for Lock, not even an explicit nil
### GetName

`func (o *Unit) GetName() string`

GetName returns the Name field if non-nil, zero value otherwise.

### GetNameOk

`func (o *Unit) GetNameOk() (*string, bool)`

GetNameOk returns a tuple with the Name field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetName

`func (o *Unit) SetName(v string)`

SetName sets Name field to given value.


### GetNamespace

`func (o *Unit) GetNamespace() string`

GetNamespace returns the Namespace field if non-nil, zero value otherwise.

### GetNamespaceOk

`func (o *Unit) GetNamespaceOk() (*string, bool)`

GetNamespaceOk returns a tuple with the Namespace field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetNamespace

`func (o *Unit) SetNamespace(v string)`

SetNamespace sets Namespace field to given value.


### GetResourceCount

`func (o *Unit) GetResourceCount() int64`

GetResourceCount returns the ResourceCount field if non-nil, zero value otherwise.

### GetResourceCountOk

`func (o *Unit) GetResourceCountOk() (*int64, bool)`

GetResourceCountOk returns a tuple with the ResourceCount field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetResourceCount

`func (o *Unit) SetResourceCount(v int64)`

SetResourceCount sets ResourceCount field to given value.

### HasResourceCount

`func (o *Unit) HasResourceCount() bool`

HasResourceCount returns a boolean if a field has been set.

### SetResourceCountNil

`func (o *Unit) SetResourceCountNil(b bool)`

 SetResourceCountNil sets the value for ResourceCount to be an explicit nil

### UnsetResourceCount
`func (o *Unit) UnsetResourceCount()`

UnsetResourceCount ensures that no value is present for ResourceCount, not even an explicit nil
### GetTool

`func (o *Unit) GetTool() Tool`

GetTool returns the Tool field if non-nil, zero value otherwise.

### GetToolOk

`func (o *Unit) GetToolOk() (*Tool, bool)`

GetToolOk returns a tuple with the Tool field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetTool

`func (o *Unit) SetTool(v Tool)`

SetTool sets Tool field to given value.


### GetUnitId

`func (o *Unit) GetUnitId() string`

GetUnitId returns the UnitId field if non-nil, zero value otherwise.

### GetUnitIdOk

`func (o *Unit) GetUnitIdOk() (*string, bool)`

GetUnitIdOk returns a tuple with the UnitId field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetUnitId

`func (o *Unit) SetUnitId(v string)`

SetUnitId sets UnitId field to given value.



[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


