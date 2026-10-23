package conformance_test

import (
	"opendustpan.org/opendustpan/internal/clientgen/conformance"
	"opendustpan.org/opendustpan/internal/clientgen/model"
)

// initParams are the parameters of initialize with nothing else set.
func initParams() conformance.InitializeParams {
	return conformance.InitializeParams{
		ProtocolVersion: conformance.ProtocolVersion, BaseURL: "http://127.0.0.1:1", Token: "",
		Transport: "", RetryLimit: 0,
	}
}

// callParams are the parameters of a call of the operation x with nothing else.
func callParams() conformance.CallParams {
	return conformance.CallParams{Operation: "x", Path: nil, Query: nil, Body: nil, IdempotencyKey: "", IfMatch: ""}
}

// paginateParams are the parameters of paginate with only the operation.
func paginateParams(op string, maxPages int) conformance.PaginateParams {
	return conformance.PaginateParams{Operation: op, Path: nil, Query: nil, PageSize: 0, MaxPages: maxPages}
}

// fault is a fault with only a kind and an operation.
func fault(kind conformance.FaultKind, op string) conformance.Fault {
	return conformance.Fault{
		Kind: kind, Operation: op, Status: 0, Body: nil, ContentType: "", RetryAfter: "", Field: "", Value: nil,
	}
}

// route is an operation with only a method and a path, for the routes of a proxy.
func route(id string, method model.Method, path string) model.Operation {
	return model.Operation{
		ID: id, Method: method, Path: path, Tags: nil, Summary: "", Description: "", Actions: nil, Public: false,
		Parameters: nil, Body: nil, Result: model.Response{Statuses: []int{204}, Type: nil, ETag: false},
		IdempotencyKey: false, IfMatch: model.IfMatchNone, Page: nil,
	}
}

// env is an Env with only a proxy.
func env(proxy conformance.Proxy) conformance.Env {
	return conformance.Env{
		Server: nil, Proxy: proxy, Adapter: nil, Transport: "", Operations: nil,
		Info: conformance.InitializeResult{
			ProtocolVersion:    0,
			Client:             "",
			ClientVersion:      "",
			DescriptionVersion: "",
			ServerRelease:      "",
			Transports:         nil,
		},
	}
}
