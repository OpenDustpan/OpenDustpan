from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.tool import Tool
from ..types import UNSET, Unset
from typing import cast
import datetime

if TYPE_CHECKING:
  from ..models.lock import Lock
  from ..models.open_tofu_details import OpenTofuDetails
  from ..models.pulumi_details import PulumiDetails
  from ..models.unit_labels import UnitLabels





T = TypeVar("T", bound="Unit")



@_attrs_define
class Unit:
    """ 
        Attributes:
            details (OpenTofuDetails | PulumiDetails):
            last_change (datetime.datetime):
            name (str):
            namespace (str):
            tool (Tool):
            unit_id (str):
            labels (UnitLabels | Unset):
            lock (Lock | None | Unset):
            resource_count (int | None | Unset):
     """

    details: OpenTofuDetails | PulumiDetails
    last_change: datetime.datetime
    name: str
    namespace: str
    tool: Tool
    unit_id: str
    labels: UnitLabels | Unset = UNSET
    lock: Lock | None | Unset = UNSET
    resource_count: int | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        from ..models.lock import Lock # noqa: PLC0415
        from ..models.open_tofu_details import OpenTofuDetails # noqa: PLC0415
        from ..models.pulumi_details import PulumiDetails # noqa: PLC0415
        from ..models.unit_labels import UnitLabels # noqa: PLC0415
        details: dict[str, Any]
        if isinstance(self.details, PulumiDetails):
            details = self.details.to_dict()
        else:
            details = self.details.to_dict()


        last_change = self.last_change.isoformat()

        name = self.name

        namespace = self.namespace

        tool = self.tool.value

        unit_id = self.unit_id

        labels: dict[str, Any] | Unset = UNSET
        if not isinstance(self.labels, Unset):
            labels = self.labels.to_dict()

        lock: dict[str, Any] | None | Unset
        if isinstance(self.lock, Unset):
            lock = UNSET
        elif isinstance(self.lock, Lock):
            lock = self.lock.to_dict()
        else:
            lock = self.lock

        resource_count: int | None | Unset
        if isinstance(self.resource_count, Unset):
            resource_count = UNSET
        else:
            resource_count = self.resource_count


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "details": details,
            "lastChange": last_change,
            "name": name,
            "namespace": namespace,
            "tool": tool,
            "unitId": unit_id,
        })
        if labels is not UNSET:
            field_dict["labels"] = labels
        if lock is not UNSET:
            field_dict["lock"] = lock
        if resource_count is not UNSET:
            field_dict["resourceCount"] = resource_count

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.lock import Lock # noqa: PLC0415
        from ..models.open_tofu_details import OpenTofuDetails # noqa: PLC0415
        from ..models.pulumi_details import PulumiDetails # noqa: PLC0415
        from ..models.unit_labels import UnitLabels # noqa: PLC0415
        d = dict(src_dict)
        def _parse_details(data: object) -> OpenTofuDetails | PulumiDetails:
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                componentsschemas_unit_details_type_0 = PulumiDetails.from_dict(data)



                return componentsschemas_unit_details_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            if not isinstance(data, dict):
                raise TypeError()
            componentsschemas_unit_details_type_1 = OpenTofuDetails.from_dict(data)



            return componentsschemas_unit_details_type_1

        details = _parse_details(d.pop("details"))


        last_change = datetime.datetime.fromisoformat(d.pop("lastChange"))




        name = d.pop("name")

        namespace = d.pop("namespace")

        tool = Tool(d.pop("tool"))




        unit_id = d.pop("unitId")

        _labels = d.pop("labels", UNSET)
        labels: UnitLabels | Unset
        if isinstance(_labels,  Unset):
            labels = UNSET
        else:
            labels = UnitLabels.from_dict(_labels)




        def _parse_lock(data: object) -> Lock | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                lock_type_0 = Lock.from_dict(data)



                return lock_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(Lock | None | Unset, data)

        lock = _parse_lock(d.pop("lock", UNSET))


        def _parse_resource_count(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        resource_count = _parse_resource_count(d.pop("resourceCount", UNSET))


        unit = cls(
            details=details,
            last_change=last_change,
            name=name,
            namespace=namespace,
            tool=tool,
            unit_id=unit_id,
            labels=labels,
            lock=lock,
            resource_count=resource_count,
        )


        unit.additional_properties = d
        return unit

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
