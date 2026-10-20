# Unit


## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**details** | [**UnitDetails**](UnitDetails.md) |  | 
**labels** | **Dict[str, str]** |  | [optional] 
**last_change** | **datetime** |  | 
**lock** | [**Lock**](Lock.md) |  | [optional] 
**name** | **str** |  | 
**namespace** | **str** |  | 
**resource_count** | **int** |  | [optional] 
**tool** | [**Tool**](Tool.md) |  | 
**unit_id** | **str** |  | 

## Example

```python
from opendustpan.models.unit import Unit

# TODO update the JSON string below
json = "{}"
# create an instance of Unit from a JSON string
unit_instance = Unit.from_json(json)
# print the JSON string representation of the object
print(Unit.to_json())

# convert the object into a dict
unit_dict = unit_instance.to_dict()
# create an instance of Unit from a dict
unit_from_dict = Unit.from_dict(unit_dict)
```
[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


