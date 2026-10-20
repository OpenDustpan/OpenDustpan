from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.verdict_decision import VerdictDecision
from ..types import UNSET, Unset
from typing import cast






T = TypeVar("T", bound="Verdict")



@_attrs_define
class Verdict:
    """ 
        Attributes:
            decision (VerdictDecision):
            comment (None | str | Unset):
     """

    decision: VerdictDecision
    comment: None | str | Unset = UNSET





    def to_dict(self) -> dict[str, Any]:
        decision = self.decision.value

        comment: None | str | Unset
        if isinstance(self.comment, Unset):
            comment = UNSET
        else:
            comment = self.comment


        field_dict: dict[str, Any] = {}

        field_dict.update({
            "decision": decision,
        })
        if comment is not UNSET:
            field_dict["comment"] = comment

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        decision = VerdictDecision(d.pop("decision"))




        def _parse_comment(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        comment = _parse_comment(d.pop("comment", UNSET))


        verdict = cls(
            decision=decision,
            comment=comment,
        )

        return verdict

