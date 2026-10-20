from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.pulumi_details_tool import PulumiDetailsTool






T = TypeVar("T", bound="PulumiDetails")



@_attrs_define
class PulumiDetails:
    """ 
        Attributes:
            project (str):
            stack (str):
            tool (PulumiDetailsTool):
     """

    project: str
    stack: str
    tool: PulumiDetailsTool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        project = self.project

        stack = self.stack

        tool = self.tool.value


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "project": project,
            "stack": stack,
            "tool": tool,
        })

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        project = d.pop("project")

        stack = d.pop("stack")

        tool = PulumiDetailsTool(d.pop("tool"))




        pulumi_details = cls(
            project=project,
            stack=stack,
            tool=tool,
        )


        pulumi_details.additional_properties = d
        return pulumi_details

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
