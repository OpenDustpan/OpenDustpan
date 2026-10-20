# Lock


## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**holder** | **str** |  | 
**operation** | **str** |  | 
**since** | **datetime** |  | 

## Example

```python
from opendustpan.models.lock import Lock

# TODO update the JSON string below
json = "{}"
# create an instance of Lock from a JSON string
lock_instance = Lock.from_json(json)
# print the JSON string representation of the object
print(Lock.to_json())

# convert the object into a dict
lock_dict = lock_instance.to_dict()
# create an instance of Lock from a dict
lock_from_dict = Lock.from_dict(lock_dict)
```
[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


