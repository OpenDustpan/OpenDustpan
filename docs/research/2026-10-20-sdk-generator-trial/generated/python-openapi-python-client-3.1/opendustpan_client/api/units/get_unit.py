from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.error import Error
from ...models.unit import Unit
from typing import cast



def _get_kwargs(
    namespace: str,
    unit_id: str,

) -> dict[str, Any]:
    

    

    

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}".format(namespace=quote(str(namespace), safe=""),unit_id=quote(str(unit_id), safe=""),),
    }


    return _kwargs



def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Error | Unit:
    if response.status_code == 200:
        response_200 = Unit.from_dict(response.json())



        return response_200

    response_default = Error.from_dict(response.json())



    return response_default



def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Error | Unit]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    namespace: str,
    unit_id: str,
    *,
    client: AuthenticatedClient | Client,

) -> Response[Error | Unit]:
    """ Read a unit

    Args:
        namespace (str):
        unit_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Error | Unit]
     """


    kwargs = _get_kwargs(
        namespace=namespace,
unit_id=unit_id,

    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)

def sync(
    namespace: str,
    unit_id: str,
    *,
    client: AuthenticatedClient | Client,

) -> Error | Unit | None:
    """ Read a unit

    Args:
        namespace (str):
        unit_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Error | Unit
     """


    return sync_detailed(
        namespace=namespace,
unit_id=unit_id,
client=client,

    ).parsed

async def asyncio_detailed(
    namespace: str,
    unit_id: str,
    *,
    client: AuthenticatedClient | Client,

) -> Response[Error | Unit]:
    """ Read a unit

    Args:
        namespace (str):
        unit_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Error | Unit]
     """


    kwargs = _get_kwargs(
        namespace=namespace,
unit_id=unit_id,

    )

    response = await client.get_async_httpx_client().request(
        **kwargs
    )

    return _build_response(client=client, response=response)

async def asyncio(
    namespace: str,
    unit_id: str,
    *,
    client: AuthenticatedClient | Client,

) -> Error | Unit | None:
    """ Read a unit

    Args:
        namespace (str):
        unit_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Error | Unit
     """


    return (await asyncio_detailed(
        namespace=namespace,
unit_id=unit_id,
client=client,

    )).parsed
