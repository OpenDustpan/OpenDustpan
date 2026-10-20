from enum import StrEnum

class ChangeOrderStatus(StrEnum):
    APPLIED = "applied"
    APPROVED = "approved"
    OPEN = "open"
    REJECTED = "rejected"

    def __str__(self) -> str:
        return str(self.value)
