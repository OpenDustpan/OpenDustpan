from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast

if TYPE_CHECKING:
  from ..models.error_error_args import ErrorErrorArgs





T = TypeVar("T", bound="Error")



@_attrs_define
class Error:
    """ The error shape of the tool-neutral API. code is the HTTP status.

        Attributes:
            code (int):
            message (str):
            error_args (ErrorErrorArgs | Unset):
            error_code (str | Unset):
     """

    code: int
    message: str
    error_args: ErrorErrorArgs | Unset = UNSET
    error_code: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        from ..models.error_error_args import ErrorErrorArgs # noqa: PLC0415
        code = self.code

        message = self.message

        error_args: dict[str, Any] | Unset = UNSET
        if not isinstance(self.error_args, Unset):
            error_args = self.error_args.to_dict()

        error_code = self.error_code


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "code": code,
            "message": message,
        })
        if error_args is not UNSET:
            field_dict["errorArgs"] = error_args
        if error_code is not UNSET:
            field_dict["errorCode"] = error_code

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.error_error_args import ErrorErrorArgs # noqa: PLC0415
        d = dict(src_dict)
        code = d.pop("code")

        message = d.pop("message")

        _error_args = d.pop("errorArgs", UNSET)
        error_args: ErrorErrorArgs | Unset
        if isinstance(_error_args,  Unset):
            error_args = UNSET
        else:
            error_args = ErrorErrorArgs.from_dict(_error_args)




        error_code = d.pop("errorCode", UNSET)

        error = cls(
            code=code,
            message=message,
            error_args=error_args,
            error_code=error_code,
        )


        error.additional_properties = d
        return error

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
