# ChangeOrder


## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**id** | **str** |  | 
**status** | **str** |  | 

## Example

```python
from opendustpan.models.change_order import ChangeOrder

# TODO update the JSON string below
json = "{}"
# create an instance of ChangeOrder from a JSON string
change_order_instance = ChangeOrder.from_json(json)
# print the JSON string representation of the object
print(ChangeOrder.to_json())

# convert the object into a dict
change_order_dict = change_order_instance.to_dict()
# create an instance of ChangeOrder from a dict
change_order_from_dict = ChangeOrder.from_dict(change_order_dict)
```
[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


