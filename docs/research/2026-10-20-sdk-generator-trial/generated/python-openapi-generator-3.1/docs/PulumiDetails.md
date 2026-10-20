# PulumiDetails


## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**project** | **str** |  | 
**stack** | **str** |  | 
**tool** | **str** |  | 

## Example

```python
from opendustpan.models.pulumi_details import PulumiDetails

# TODO update the JSON string below
json = "{}"
# create an instance of PulumiDetails from a JSON string
pulumi_details_instance = PulumiDetails.from_json(json)
# print the JSON string representation of the object
print(PulumiDetails.to_json())

# convert the object into a dict
pulumi_details_dict = pulumi_details_instance.to_dict()
# create an instance of PulumiDetails from a dict
pulumi_details_from_dict = PulumiDetails.from_dict(pulumi_details_dict)
```
[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


