# Verdict

## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**Comment** | Pointer to **NullableString** |  | [optional] 
**Decision** | **string** |  | 

## Methods

### NewVerdict

`func NewVerdict(decision string, ) *Verdict`

NewVerdict instantiates a new Verdict object
This constructor will assign default values to properties that have it defined,
and makes sure properties required by API are set, but the set of arguments
will change when the set of required properties is changed

### NewVerdictWithDefaults

`func NewVerdictWithDefaults() *Verdict`

NewVerdictWithDefaults instantiates a new Verdict object
This constructor will only assign default values to properties that have it defined,
but it doesn't guarantee that properties required by API are set

### GetComment

`func (o *Verdict) GetComment() string`

GetComment returns the Comment field if non-nil, zero value otherwise.

### GetCommentOk

`func (o *Verdict) GetCommentOk() (*string, bool)`

GetCommentOk returns a tuple with the Comment field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetComment

`func (o *Verdict) SetComment(v string)`

SetComment sets Comment field to given value.

### HasComment

`func (o *Verdict) HasComment() bool`

HasComment returns a boolean if a field has been set.

### SetCommentNil

`func (o *Verdict) SetCommentNil(b bool)`

 SetCommentNil sets the value for Comment to be an explicit nil

### UnsetComment
`func (o *Verdict) UnsetComment()`

UnsetComment ensures that no value is present for Comment, not even an explicit nil
### GetDecision

`func (o *Verdict) GetDecision() string`

GetDecision returns the Decision field if non-nil, zero value otherwise.

### GetDecisionOk

`func (o *Verdict) GetDecisionOk() (*string, bool)`

GetDecisionOk returns a tuple with the Decision field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetDecision

`func (o *Verdict) SetDecision(v string)`

SetDecision sets Decision field to given value.



[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


