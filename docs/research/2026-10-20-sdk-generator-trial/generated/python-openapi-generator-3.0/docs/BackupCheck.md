# BackupCheck


## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**ok** | **bool** |  | 
**problems** | **List[str]** |  | 

## Example

```python
from opendustpan.models.backup_check import BackupCheck

# TODO update the JSON string below
json = "{}"
# create an instance of BackupCheck from a JSON string
backup_check_instance = BackupCheck.from_json(json)
# print the JSON string representation of the object
print(BackupCheck.to_json())

# convert the object into a dict
backup_check_dict = backup_check_instance.to_dict()
# create an instance of BackupCheck from a dict
backup_check_from_dict = BackupCheck.from_dict(backup_check_dict)
```
[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


