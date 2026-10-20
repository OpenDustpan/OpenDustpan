# UnitList


## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**next_page_token** | **str** |  | 
**units** | [**List[Unit]**](Unit.md) |  | 

## Example

```python
from opendustpan.models.unit_list import UnitList

# TODO update the JSON string below
json = "{}"
# create an instance of UnitList from a JSON string
unit_list_instance = UnitList.from_json(json)
# print the JSON string representation of the object
print(UnitList.to_json())

# convert the object into a dict
unit_list_dict = unit_list_instance.to_dict()
# create an instance of UnitList from a dict
unit_list_from_dict = UnitList.from_dict(unit_list_dict)
```
[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


