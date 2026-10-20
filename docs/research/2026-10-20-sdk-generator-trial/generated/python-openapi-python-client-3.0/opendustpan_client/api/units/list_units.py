from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.error import Error
from ...models.tool import Tool
from ...models.unit_list import UnitList
from ...types import UNSET, Unset
from typing import cast



def _get_kwargs(
    namespace: str,
    *,
    page_size: int | Unset = 50,
    page_token: str | Unset = UNSET,
    tool: Tool | Unset = UNSET,

) -> dict[str, Any]:
    

    

    params: dict[str, Any] = {}

    params["pageSize"] = page_size

    params["pageToken"] = page_token

    json_tool: str | Unset = UNSET
    if not isinstance(tool, Unset):
        json_tool = tool.value

    params["tool"] = json_tool


    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}


    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/api/dustpan/v1/namespaces/{namespace}/units".format(namespace=quote(str(namespace), safe=""),),
        "params": params,
    }


    return _kwargs



def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Error | UnitList:
    if response.status_code == 200:
        response_200 = UnitList.from_dict(response.json())



        return response_200

    response_default = Error.from_dict(response.json())



    return response_default



def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Error | UnitList]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    namespace: str,
    *,
    client: AuthenticatedClient | Client,
    page_size: int | Unset = 50,
    page_token: str | Unset = UNSET,
    tool: Tool | Unset = UNSET,

) -> Response[Error | UnitList]:
    """ List the units of a namespace

    Args:
        namespace (str):
        page_size (int | Unset):  Default: 50.
        page_token (str | Unset):
        tool (Tool | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Error | UnitList]
     """


    kwargs = _get_kwargs(
        namespace=namespace,
page_size=page_size,
page_token=page_token,
tool=tool,

    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)

def sync(
    namespace: str,
    *,
    client: AuthenticatedClient | Client,
    page_size: int | Unset = 50,
    page_token: str | Unset = UNSET,
    tool: Tool | Unset = UNSET,

) -> Error | UnitList | None:
    """ List the units of a namespace

    Args:
        namespace (str):
        page_size (int | Unset):  Default: 50.
        page_token (str | Unset):
        tool (Tool | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Error | UnitList
     """


    return sync_detailed(
        namespace=namespace,
client=client,
page_size=page_size,
page_token=page_token,
tool=tool,

    ).parsed

async def asyncio_detailed(
    namespace: str,
    *,
    client: AuthenticatedClient | Client,
    page_size: int | Unset = 50,
    page_token: str | Unset = UNSET,
    tool: Tool | Unset = UNSET,

) -> Response[Error | UnitList]:
    """ List the units of a namespace

    Args:
        namespace (str):
        page_size (int | Unset):  Default: 50.
        page_token (str | Unset):
        tool (Tool | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Error | UnitList]
     """


    kwargs = _get_kwargs(
        namespace=namespace,
page_size=page_size,
page_token=page_token,
tool=tool,

    )

    response = await client.get_async_httpx_client().request(
        **kwargs
    )

    return _build_response(client=client, response=response)

async def asyncio(
    namespace: str,
    *,
    client: AuthenticatedClient | Client,
    page_size: int | Unset = 50,
    page_token: str | Unset = UNSET,
    tool: Tool | Unset = UNSET,

) -> Error | UnitList | None:
    """ List the units of a namespace

    Args:
        namespace (str):
        page_size (int | Unset):  Default: 50.
        page_token (str | Unset):
        tool (Tool | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Error | UnitList
     """


    return (await asyncio_detailed(
        namespace=namespace,
client=client,
page_size=page_size,
page_token=page_token,
tool=tool,

    )).parsed
