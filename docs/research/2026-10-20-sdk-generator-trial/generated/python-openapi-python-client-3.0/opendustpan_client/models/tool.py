from enum import StrEnum

class Tool(StrEnum):
    OPENTOFU = "opentofu"
    PULUMI = "pulumi"

    def __str__(self) -> str:
        return str(self.value)
