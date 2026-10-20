# UnitList

## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**NextPageToken** | **string** |  | 
**Units** | [**[]Unit**](Unit.md) |  | 

## Methods

### NewUnitList

`func NewUnitList(nextPageToken string, units []Unit, ) *UnitList`

NewUnitList instantiates a new UnitList object
This constructor will assign default values to properties that have it defined,
and makes sure properties required by API are set, but the set of arguments
will change when the set of required properties is changed

### NewUnitListWithDefaults

`func NewUnitListWithDefaults() *UnitList`

NewUnitListWithDefaults instantiates a new UnitList object
This constructor will only assign default values to properties that have it defined,
but it doesn't guarantee that properties required by API are set

### GetNextPageToken

`func (o *UnitList) GetNextPageToken() string`

GetNextPageToken returns the NextPageToken field if non-nil, zero value otherwise.

### GetNextPageTokenOk

`func (o *UnitList) GetNextPageTokenOk() (*string, bool)`

GetNextPageTokenOk returns a tuple with the NextPageToken field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetNextPageToken

`func (o *UnitList) SetNextPageToken(v string)`

SetNextPageToken sets NextPageToken field to given value.


### GetUnits

`func (o *UnitList) GetUnits() []Unit`

GetUnits returns the Units field if non-nil, zero value otherwise.

### GetUnitsOk

`func (o *UnitList) GetUnitsOk() (*[]Unit, bool)`

GetUnitsOk returns a tuple with the Units field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetUnits

`func (o *UnitList) SetUnits(v []Unit)`

SetUnits sets Units field to given value.



[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


