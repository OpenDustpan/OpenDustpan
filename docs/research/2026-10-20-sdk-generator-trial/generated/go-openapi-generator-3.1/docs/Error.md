# Error

## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**Code** | **int32** |  | 
**ErrorArgs** | Pointer to [**map[string]ErrorErrorArgsValue**](ErrorErrorArgsValue.md) |  | [optional] 
**ErrorCode** | Pointer to **string** |  | [optional] 
**Message** | **string** |  | 

## Methods

### NewError

`func NewError(code int32, message string, ) *Error`

NewError instantiates a new Error object
This constructor will assign default values to properties that have it defined,
and makes sure properties required by API are set, but the set of arguments
will change when the set of required properties is changed

### NewErrorWithDefaults

`func NewErrorWithDefaults() *Error`

NewErrorWithDefaults instantiates a new Error object
This constructor will only assign default values to properties that have it defined,
but it doesn't guarantee that properties required by API are set

### GetCode

`func (o *Error) GetCode() int32`

GetCode returns the Code field if non-nil, zero value otherwise.

### GetCodeOk

`func (o *Error) GetCodeOk() (*int32, bool)`

GetCodeOk returns a tuple with the Code field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetCode

`func (o *Error) SetCode(v int32)`

SetCode sets Code field to given value.


### GetErrorArgs

`func (o *Error) GetErrorArgs() map[string]ErrorErrorArgsValue`

GetErrorArgs returns the ErrorArgs field if non-nil, zero value otherwise.

### GetErrorArgsOk

`func (o *Error) GetErrorArgsOk() (*map[string]ErrorErrorArgsValue, bool)`

GetErrorArgsOk returns a tuple with the ErrorArgs field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetErrorArgs

`func (o *Error) SetErrorArgs(v map[string]ErrorErrorArgsValue)`

SetErrorArgs sets ErrorArgs field to given value.

### HasErrorArgs

`func (o *Error) HasErrorArgs() bool`

HasErrorArgs returns a boolean if a field has been set.

### GetErrorCode

`func (o *Error) GetErrorCode() string`

GetErrorCode returns the ErrorCode field if non-nil, zero value otherwise.

### GetErrorCodeOk

`func (o *Error) GetErrorCodeOk() (*string, bool)`

GetErrorCodeOk returns a tuple with the ErrorCode field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetErrorCode

`func (o *Error) SetErrorCode(v string)`

SetErrorCode sets ErrorCode field to given value.

### HasErrorCode

`func (o *Error) HasErrorCode() bool`

HasErrorCode returns a boolean if a field has been set.

### GetMessage

`func (o *Error) GetMessage() string`

GetMessage returns the Message field if non-nil, zero value otherwise.

### GetMessageOk

`func (o *Error) GetMessageOk() (*string, bool)`

GetMessageOk returns a tuple with the Message field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetMessage

`func (o *Error) SetMessage(v string)`

SetMessage sets Message field to given value.



[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


