package convert

import "go-match/proto/gen"

func CommonErrRespMeta(err error) *gen.RespMeta {
	return &gen.RespMeta{
		Code: -1,
		Msg:  err.Error(),
	}
}

func CommonRespMeta() *gen.RespMeta {
	return &gen.RespMeta{
		Code: 0,
		Msg:  "success",
	}
}
