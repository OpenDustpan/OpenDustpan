# Preferences

## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**Extra** | Pointer to **map[string]interface{}** |  | [optional] 
**Language** | Pointer to **NullableString** |  | [optional] 
**PinnedUnits** | Pointer to **[]string** |  | [optional] 
**Theme** | Pointer to **string** |  | [optional] 

## Methods

### NewPreferences

`func NewPreferences() *Preferences`

NewPreferences instantiates a new Preferences object
This constructor will assign default values to properties that have it defined,
and makes sure properties required by API are set, but the set of arguments
will change when the set of required properties is changed

### NewPreferencesWithDefaults

`func NewPreferencesWithDefaults() *Preferences`

NewPreferencesWithDefaults instantiates a new Preferences object
This constructor will only assign default values to properties that have it defined,
but it doesn't guarantee that properties required by API are set

### GetExtra

`func (o *Preferences) GetExtra() map[string]interface{}`

GetExtra returns the Extra field if non-nil, zero value otherwise.

### GetExtraOk

`func (o *Preferences) GetExtraOk() (*map[string]interface{}, bool)`

GetExtraOk returns a tuple with the Extra field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetExtra

`func (o *Preferences) SetExtra(v map[string]interface{})`

SetExtra sets Extra field to given value.

### HasExtra

`func (o *Preferences) HasExtra() bool`

HasExtra returns a boolean if a field has been set.

### GetLanguage

`func (o *Preferences) GetLanguage() string`

GetLanguage returns the Language field if non-nil, zero value otherwise.

### GetLanguageOk

`func (o *Preferences) GetLanguageOk() (*string, bool)`

GetLanguageOk returns a tuple with the Language field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetLanguage

`func (o *Preferences) SetLanguage(v string)`

SetLanguage sets Language field to given value.

### HasLanguage

`func (o *Preferences) HasLanguage() bool`

HasLanguage returns a boolean if a field has been set.

### SetLanguageNil

`func (o *Preferences) SetLanguageNil(b bool)`

 SetLanguageNil sets the value for Language to be an explicit nil

### UnsetLanguage
`func (o *Preferences) UnsetLanguage()`

UnsetLanguage ensures that no value is present for Language, not even an explicit nil
### GetPinnedUnits

`func (o *Preferences) GetPinnedUnits() []string`

GetPinnedUnits returns the PinnedUnits field if non-nil, zero value otherwise.

### GetPinnedUnitsOk

`func (o *Preferences) GetPinnedUnitsOk() (*[]string, bool)`

GetPinnedUnitsOk returns a tuple with the PinnedUnits field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetPinnedUnits

`func (o *Preferences) SetPinnedUnits(v []string)`

SetPinnedUnits sets PinnedUnits field to given value.

### HasPinnedUnits

`func (o *Preferences) HasPinnedUnits() bool`

HasPinnedUnits returns a boolean if a field has been set.

### GetTheme

`func (o *Preferences) GetTheme() string`

GetTheme returns the Theme field if non-nil, zero value otherwise.

### GetThemeOk

`func (o *Preferences) GetThemeOk() (*string, bool)`

GetThemeOk returns a tuple with the Theme field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetTheme

`func (o *Preferences) SetTheme(v string)`

SetTheme sets Theme field to given value.

### HasTheme

`func (o *Preferences) HasTheme() bool`

HasTheme returns a boolean if a field has been set.


[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


