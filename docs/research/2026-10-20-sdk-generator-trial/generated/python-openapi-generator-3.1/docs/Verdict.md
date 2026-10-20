# Verdict


## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**comment** | **str** |  | [optional] 
**decision** | **str** |  | 

## Example

```python
from opendustpan.models.verdict import Verdict

# TODO update the JSON string below
json = "{}"
# create an instance of Verdict from a JSON string
verdict_instance = Verdict.from_json(json)
# print the JSON string representation of the object
print(Verdict.to_json())

# convert the object into a dict
verdict_dict = verdict_instance.to_dict()
# create an instance of Verdict from a dict
verdict_from_dict = Verdict.from_dict(verdict_dict)
```
[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


