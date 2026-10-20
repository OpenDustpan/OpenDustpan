from enum import StrEnum

class OpenTofuDetailsTool(StrEnum):
    OPENTOFU = "opentofu"

    def __str__(self) -> str:
        return str(self.value)
