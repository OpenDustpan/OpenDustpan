# Lock

## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**Holder** | **string** |  | 
**Operation** | **string** |  | 
**Since** | **time.Time** |  | 

## Methods

### NewLock

`func NewLock(holder string, operation string, since time.Time, ) *Lock`

NewLock instantiates a new Lock object
This constructor will assign default values to properties that have it defined,
and makes sure properties required by API are set, but the set of arguments
will change when the set of required properties is changed

### NewLockWithDefaults

`func NewLockWithDefaults() *Lock`

NewLockWithDefaults instantiates a new Lock object
This constructor will only assign default values to properties that have it defined,
but it doesn't guarantee that properties required by API are set

### GetHolder

`func (o *Lock) GetHolder() string`

GetHolder returns the Holder field if non-nil, zero value otherwise.

### GetHolderOk

`func (o *Lock) GetHolderOk() (*string, bool)`

GetHolderOk returns a tuple with the Holder field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetHolder

`func (o *Lock) SetHolder(v string)`

SetHolder sets Holder field to given value.


### GetOperation

`func (o *Lock) GetOperation() string`

GetOperation returns the Operation field if non-nil, zero value otherwise.

### GetOperationOk

`func (o *Lock) GetOperationOk() (*string, bool)`

GetOperationOk returns a tuple with the Operation field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetOperation

`func (o *Lock) SetOperation(v string)`

SetOperation sets Operation field to given value.


### GetSince

`func (o *Lock) GetSince() time.Time`

GetSince returns the Since field if non-nil, zero value otherwise.

### GetSinceOk

`func (o *Lock) GetSinceOk() (*time.Time, bool)`

GetSinceOk returns a tuple with the Since field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetSince

`func (o *Lock) SetSince(v time.Time)`

SetSince sets Since field to given value.



[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


