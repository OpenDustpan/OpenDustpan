# OpenTofuDetails


## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**terraform_version** | **str** |  | [optional] 
**tool** | **str** |  | 
**workspace** | **str** |  | 

## Example

```python
from opendustpan.models.open_tofu_details import OpenTofuDetails

# TODO update the JSON string below
json = "{}"
# create an instance of OpenTofuDetails from a JSON string
open_tofu_details_instance = OpenTofuDetails.from_json(json)
# print the JSON string representation of the object
print(OpenTofuDetails.to_json())

# convert the object into a dict
open_tofu_details_dict = open_tofu_details_instance.to_dict()
# create an instance of OpenTofuDetails from a dict
open_tofu_details_from_dict = OpenTofuDetails.from_dict(open_tofu_details_dict)
```
[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


