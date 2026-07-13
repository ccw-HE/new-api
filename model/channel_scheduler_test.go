package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
)

func TestResolveSchedulerRetryTimesBoundsInvalidValues(t *testing.T) {
	const globalDefault = 3

	tests := []struct {
		name  string
		value *int
		want  int
	}{
		{name: "nil uses global default", value: nil, want: globalDefault},
		{name: "zero uses global default", value: common.GetPointer(0), want: globalDefault},
		{name: "negative uses global default", value: common.GetPointer(-1), want: globalDefault},
		{name: "over maximum uses global default", value: common.GetPointer(101), want: globalDefault},
		{name: "maximum is accepted", value: common.GetPointer(100), want: 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := &Channel{SchedulerRetryTimes: tt.value}

			assert.Equal(t, tt.want, channel.ResolveSchedulerRetryTimes(globalDefault))
		})
	}
}
