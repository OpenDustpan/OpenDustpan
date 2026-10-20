# BackupCheck

## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**Ok** | **bool** |  | 
**Problems** | **[]string** |  | 

## Methods

### NewBackupCheck

`func NewBackupCheck(ok bool, problems []string, ) *BackupCheck`

NewBackupCheck instantiates a new BackupCheck object
This constructor will assign default values to properties that have it defined,
and makes sure properties required by API are set, but the set of arguments
will change when the set of required properties is changed

### NewBackupCheckWithDefaults

`func NewBackupCheckWithDefaults() *BackupCheck`

NewBackupCheckWithDefaults instantiates a new BackupCheck object
This constructor will only assign default values to properties that have it defined,
but it doesn't guarantee that properties required by API are set

### GetOk

`func (o *BackupCheck) GetOk() bool`

GetOk returns the Ok field if non-nil, zero value otherwise.

### GetOkOk

`func (o *BackupCheck) GetOkOk() (*bool, bool)`

GetOkOk returns a tuple with the Ok field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetOk

`func (o *BackupCheck) SetOk(v bool)`

SetOk sets Ok field to given value.


### GetProblems

`func (o *BackupCheck) GetProblems() []string`

GetProblems returns the Problems field if non-nil, zero value otherwise.

### GetProblemsOk

`func (o *BackupCheck) GetProblemsOk() (*[]string, bool)`

GetProblemsOk returns a tuple with the Problems field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetProblems

`func (o *BackupCheck) SetProblems(v []string)`

SetProblems sets Problems field to given value.



[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


