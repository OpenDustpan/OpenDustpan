# UnitDetails


## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**project** | **str** |  | 
**stack** | **str** |  | 
**tool** | **str** |  | 
**terraform_version** | **str** |  | [optional] 
**workspace** | **str** |  | 

## Example

```python
from opendustpan.models.unit_details import UnitDetails

# TODO update the JSON string below
json = "{}"
# create an instance of UnitDetails from a JSON string
unit_details_instance = UnitDetails.from_json(json)
# print the JSON string representation of the object
print(UnitDetails.to_json())

# convert the object into a dict
unit_details_dict = unit_details_instance.to_dict()
# create an instance of UnitDetails from a dict
unit_details_from_dict = UnitDetails.from_dict(unit_details_dict)
```
[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


