from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.preferences_theme import PreferencesTheme
from ..types import UNSET, Unset
from typing import cast

if TYPE_CHECKING:
  from ..models.preferences_extra import PreferencesExtra





T = TypeVar("T", bound="Preferences")



@_attrs_define
class Preferences:
    """ 
        Attributes:
            extra (PreferencesExtra | Unset):
            language (None | str | Unset):
            pinned_units (list[str] | Unset):
            theme (PreferencesTheme | Unset):
     """

    extra: PreferencesExtra | Unset = UNSET
    language: None | str | Unset = UNSET
    pinned_units: list[str] | Unset = UNSET
    theme: PreferencesTheme | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        from ..models.preferences_extra import PreferencesExtra # noqa: PLC0415
        extra: dict[str, Any] | Unset = UNSET
        if not isinstance(self.extra, Unset):
            extra = self.extra.to_dict()

        language: None | str | Unset
        if isinstance(self.language, Unset):
            language = UNSET
        else:
            language = self.language

        pinned_units: list[str] | Unset = UNSET
        if not isinstance(self.pinned_units, Unset):
            pinned_units = self.pinned_units



        theme: str | Unset = UNSET
        if not isinstance(self.theme, Unset):
            theme = self.theme.value



        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
        })
        if extra is not UNSET:
            field_dict["extra"] = extra
        if language is not UNSET:
            field_dict["language"] = language
        if pinned_units is not UNSET:
            field_dict["pinnedUnits"] = pinned_units
        if theme is not UNSET:
            field_dict["theme"] = theme

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.preferences_extra import PreferencesExtra # noqa: PLC0415
        d = dict(src_dict)
        _extra = d.pop("extra", UNSET)
        extra: PreferencesExtra | Unset
        if isinstance(_extra,  Unset):
            extra = UNSET
        else:
            extra = PreferencesExtra.from_dict(_extra)




        def _parse_language(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        language = _parse_language(d.pop("language", UNSET))


        pinned_units = cast(list[str], d.pop("pinnedUnits", UNSET))


        _theme = d.pop("theme", UNSET)
        theme: PreferencesTheme | Unset
        if isinstance(_theme,  Unset):
            theme = UNSET
        else:
            theme = PreferencesTheme(_theme)




        preferences = cls(
            extra=extra,
            language=language,
            pinned_units=pinned_units,
            theme=theme,
        )


        preferences.additional_properties = d
        return preferences

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
