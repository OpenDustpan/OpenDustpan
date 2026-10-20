from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast

if TYPE_CHECKING:
  from ..models.module import Module





T = TypeVar("T", bound="Capabilities")



@_attrs_define
class Capabilities:
    """ 
        Attributes:
            api_version (str):
            modules (list[Module] | Unset):
     """

    api_version: str
    modules: list[Module] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        from ..models.module import Module # noqa: PLC0415
        api_version = self.api_version

        modules: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.modules, Unset):
            modules = []
            for modules_item_data in self.modules:
                modules_item = modules_item_data.to_dict()
                modules.append(modules_item)




        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "apiVersion": api_version,
        })
        if modules is not UNSET:
            field_dict["modules"] = modules

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.module import Module # noqa: PLC0415
        d = dict(src_dict)
        api_version = d.pop("apiVersion")

        _modules = d.pop("modules", UNSET)
        modules: list[Module] | Unset = UNSET
        if _modules is not UNSET:
            modules = []
            for modules_item_data in _modules:
                modules_item = Module.from_dict(modules_item_data)



                modules.append(modules_item)


        capabilities = cls(
            api_version=api_version,
            modules=modules,
        )


        capabilities.additional_properties = d
        return capabilities

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
