package provider

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
)

func Test_overrideEnvVars(t *testing.T) {
	type args struct {
		orig []corev1.EnvVar
		new  []corev1.EnvVar
	}

	tests := []struct {
		name string
		args args
		want []corev1.EnvVar
	}{
		{
			name: "orig and new are empty",
			args: args{
				orig: []v1.EnvVar{},
				new:  []v1.EnvVar{},
			},
			want: []v1.EnvVar{},
		},
		{
			name: "only orig is empty",
			args: args{
				orig: []v1.EnvVar{},
				new:  []v1.EnvVar{{Name: "FOO", Value: "new_val"}},
			},
			want: []v1.EnvVar{},
		},
		{
			name: "orig has a matching element",
			args: args{
				orig: []v1.EnvVar{{Name: "FOO", Value: "old_val"}},
				new:  []v1.EnvVar{{Name: "FOO", Value: "new_val"}},
			},
			want: []v1.EnvVar{{Name: "FOO", Value: "new_val"}},
		},
		{
			name: "orig have multiple elements",
			args: args{
				orig: []v1.EnvVar{{Name: "FOO_0", Value: "old_val_0"}, {Name: "FOO_1", Value: "old_val_1"}},
				new:  []v1.EnvVar{{Name: "FOO_1", Value: "new_val_1"}},
			},
			want: []v1.EnvVar{{Name: "FOO_0", Value: "old_val_0"}, {Name: "FOO_1", Value: "new_val_1"}},
		},
		{
			name: "orig and new have multiple elements and some not matching",
			args: args{
				orig: []v1.EnvVar{{Name: "FOO_0", Value: "old_val_0"}, {Name: "FOO_1", Value: "old_val_1"}},
				new:  []v1.EnvVar{{Name: "FOO_1", Value: "new_val_1"}, {Name: "FOO_2", Value: "val_1"}},
			},
			want: []v1.EnvVar{{Name: "FOO_0", Value: "old_val_0"}, {Name: "FOO_1", Value: "new_val_1"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := overrideEnvVars(tt.args.orig, tt.args.new); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("overrideEnvVars() = %v, want %v", got, tt.want)
			}
		})
	}
}
