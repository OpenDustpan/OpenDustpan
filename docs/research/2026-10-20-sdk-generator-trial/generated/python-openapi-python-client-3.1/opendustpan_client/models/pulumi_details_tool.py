from enum import StrEnum

class PulumiDetailsTool(StrEnum):
    PULUMI = "pulumi"

    def __str__(self) -> str:
        return str(self.value)
