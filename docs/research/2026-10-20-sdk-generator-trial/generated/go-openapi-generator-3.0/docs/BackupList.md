# BackupList

## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**Backups** | [**[]Backup**](Backup.md) |  | 
**NextPageToken** | **string** |  | 

## Methods

### NewBackupList

`func NewBackupList(backups []Backup, nextPageToken string, ) *BackupList`

NewBackupList instantiates a new BackupList object
This constructor will assign default values to properties that have it defined,
and makes sure properties required by API are set, but the set of arguments
will change when the set of required properties is changed

### NewBackupListWithDefaults

`func NewBackupListWithDefaults() *BackupList`

NewBackupListWithDefaults instantiates a new BackupList object
This constructor will only assign default values to properties that have it defined,
but it doesn't guarantee that properties required by API are set

### GetBackups

`func (o *BackupList) GetBackups() []Backup`

GetBackups returns the Backups field if non-nil, zero value otherwise.

### GetBackupsOk

`func (o *BackupList) GetBackupsOk() (*[]Backup, bool)`

GetBackupsOk returns a tuple with the Backups field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetBackups

`func (o *BackupList) SetBackups(v []Backup)`

SetBackups sets Backups field to given value.


### GetNextPageToken

`func (o *BackupList) GetNextPageToken() string`

GetNextPageToken returns the NextPageToken field if non-nil, zero value otherwise.

### GetNextPageTokenOk

`func (o *BackupList) GetNextPageTokenOk() (*string, bool)`

GetNextPageTokenOk returns a tuple with the NextPageToken field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetNextPageToken

`func (o *BackupList) SetNextPageToken(v string)`

SetNextPageToken sets NextPageToken field to given value.



[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


