from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from typing import cast

if TYPE_CHECKING:
  from ..models.unit import Unit





T = TypeVar("T", bound="UnitList")



@_attrs_define
class UnitList:
    """ 
        Attributes:
            next_page_token (str):
            units (list[Unit]):
     """

    next_page_token: str
    units: list[Unit]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        from ..models.unit import Unit # noqa: PLC0415
        next_page_token = self.next_page_token

        units = []
        for units_item_data in self.units:
            units_item = units_item_data.to_dict()
            units.append(units_item)




        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "nextPageToken": next_page_token,
            "units": units,
        })

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.unit import Unit # noqa: PLC0415
        d = dict(src_dict)
        next_page_token = d.pop("nextPageToken")

        units = []
        _units = d.pop("units")
        for units_item_data in (_units):
            units_item = Unit.from_dict(units_item_data)



            units.append(units_item)


        unit_list = cls(
            next_page_token=next_page_token,
            units=units,
        )


        unit_list.additional_properties = d
        return unit_list

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
