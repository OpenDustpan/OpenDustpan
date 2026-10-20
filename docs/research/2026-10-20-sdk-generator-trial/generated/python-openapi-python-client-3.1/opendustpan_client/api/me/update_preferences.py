from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.error import Error
from ...models.preferences import Preferences
from ...types import UNSET, Unset
from typing import cast



def _get_kwargs(
    *,
    body: Preferences,
    idempotency_key: str | Unset = UNSET,
    if_match: str | Unset = UNSET,

) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    if not isinstance(if_match, Unset):
        headers["If-Match"] = if_match



    

    

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/api/dustpan/v1/me/preferences",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/merge-patch+json"

    _kwargs["headers"] = headers
    return _kwargs



def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Error | Preferences:
    if response.status_code == 200:
        response_200 = Preferences.from_dict(response.json())



        return response_200

    response_default = Error.from_dict(response.json())



    return response_default



def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Error | Preferences]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: Preferences,
    idempotency_key: str | Unset = UNSET,
    if_match: str | Unset = UNSET,

) -> Response[Error | Preferences]:
    """ Change the preferences of the caller

    Args:
        idempotency_key (str | Unset):
        if_match (str | Unset):
        body (Preferences):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Error | Preferences]
     """


    kwargs = _get_kwargs(
        body=body,
idempotency_key=idempotency_key,
if_match=if_match,

    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)

def sync(
    *,
    client: AuthenticatedClient | Client,
    body: Preferences,
    idempotency_key: str | Unset = UNSET,
    if_match: str | Unset = UNSET,

) -> Error | Preferences | None:
    """ Change the preferences of the caller

    Args:
        idempotency_key (str | Unset):
        if_match (str | Unset):
        body (Preferences):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Error | Preferences
     """


    return sync_detailed(
        client=client,
body=body,
idempotency_key=idempotency_key,
if_match=if_match,

    ).parsed

async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: Preferences,
    idempotency_key: str | Unset = UNSET,
    if_match: str | Unset = UNSET,

) -> Response[Error | Preferences]:
    """ Change the preferences of the caller

    Args:
        idempotency_key (str | Unset):
        if_match (str | Unset):
        body (Preferences):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Error | Preferences]
     """


    kwargs = _get_kwargs(
        body=body,
idempotency_key=idempotency_key,
if_match=if_match,

    )

    response = await client.get_async_httpx_client().request(
        **kwargs
    )

    return _build_response(client=client, response=response)

async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: Preferences,
    idempotency_key: str | Unset = UNSET,
    if_match: str | Unset = UNSET,

) -> Error | Preferences | None:
    """ Change the preferences of the caller

    Args:
        idempotency_key (str | Unset):
        if_match (str | Unset):
        body (Preferences):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Error | Preferences
     """


    return (await asyncio_detailed(
        client=client,
body=body,
idempotency_key=idempotency_key,
if_match=if_match,

    )).parsed
