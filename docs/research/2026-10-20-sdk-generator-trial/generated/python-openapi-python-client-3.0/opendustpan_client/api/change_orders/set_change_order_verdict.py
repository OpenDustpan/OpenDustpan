from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.change_order import ChangeOrder
from ...models.error import Error
from ...models.verdict import Verdict
from ...types import UNSET, Unset
from typing import cast



def _get_kwargs(
    namespace: str,
    id: str,
    *,
    body: Verdict,
    idempotency_key: str | Unset = UNSET,

) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key



    

    

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/api/dustpan/v1/namespaces/{namespace}/change-orders/{id}/verdict".format(namespace=quote(str(namespace), safe=""),id=quote(str(id), safe=""),),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs



def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ChangeOrder | Error:
    if response.status_code == 200:
        response_200 = ChangeOrder.from_dict(response.json())



        return response_200

    response_default = Error.from_dict(response.json())



    return response_default



def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[ChangeOrder | Error]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    namespace: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: Verdict,
    idempotency_key: str | Unset = UNSET,

) -> Response[ChangeOrder | Error]:
    """ Approve or reject a change order

    Args:
        namespace (str):
        id (str):
        idempotency_key (str | Unset):
        body (Verdict):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ChangeOrder | Error]
     """


    kwargs = _get_kwargs(
        namespace=namespace,
id=id,
body=body,
idempotency_key=idempotency_key,

    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)

def sync(
    namespace: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: Verdict,
    idempotency_key: str | Unset = UNSET,

) -> ChangeOrder | Error | None:
    """ Approve or reject a change order

    Args:
        namespace (str):
        id (str):
        idempotency_key (str | Unset):
        body (Verdict):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ChangeOrder | Error
     """


    return sync_detailed(
        namespace=namespace,
id=id,
client=client,
body=body,
idempotency_key=idempotency_key,

    ).parsed

async def asyncio_detailed(
    namespace: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: Verdict,
    idempotency_key: str | Unset = UNSET,

) -> Response[ChangeOrder | Error]:
    """ Approve or reject a change order

    Args:
        namespace (str):
        id (str):
        idempotency_key (str | Unset):
        body (Verdict):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ChangeOrder | Error]
     """


    kwargs = _get_kwargs(
        namespace=namespace,
id=id,
body=body,
idempotency_key=idempotency_key,

    )

    response = await client.get_async_httpx_client().request(
        **kwargs
    )

    return _build_response(client=client, response=response)

async def asyncio(
    namespace: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: Verdict,
    idempotency_key: str | Unset = UNSET,

) -> ChangeOrder | Error | None:
    """ Approve or reject a change order

    Args:
        namespace (str):
        id (str):
        idempotency_key (str | Unset):
        body (Verdict):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ChangeOrder | Error
     """


    return (await asyncio_detailed(
        namespace=namespace,
id=id,
client=client,
body=body,
idempotency_key=idempotency_key,

    )).parsed
