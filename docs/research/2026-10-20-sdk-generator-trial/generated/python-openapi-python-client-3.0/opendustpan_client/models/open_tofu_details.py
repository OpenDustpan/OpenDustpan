from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.open_tofu_details_tool import OpenTofuDetailsTool
from ..types import UNSET, Unset
from typing import cast






T = TypeVar("T", bound="OpenTofuDetails")



@_attrs_define
class OpenTofuDetails:
    """ 
        Attributes:
            tool (OpenTofuDetailsTool):
            workspace (str):
            terraform_version (None | str | Unset):
     """

    tool: OpenTofuDetailsTool
    workspace: str
    terraform_version: None | str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        tool = self.tool.value

        workspace = self.workspace

        terraform_version: None | str | Unset
        if isinstance(self.terraform_version, Unset):
            terraform_version = UNSET
        else:
            terraform_version = self.terraform_version


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "tool": tool,
            "workspace": workspace,
        })
        if terraform_version is not UNSET:
            field_dict["terraformVersion"] = terraform_version

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        tool = OpenTofuDetailsTool(d.pop("tool"))




        workspace = d.pop("workspace")

        def _parse_terraform_version(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        terraform_version = _parse_terraform_version(d.pop("terraformVersion", UNSET))


        open_tofu_details = cls(
            tool=tool,
            workspace=workspace,
            terraform_version=terraform_version,
        )


        open_tofu_details.additional_properties = d
        return open_tofu_details

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
