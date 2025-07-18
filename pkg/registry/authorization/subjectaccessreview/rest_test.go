/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package subjectaccessreview

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	authorizationapi "k8s.io/kubernetes/pkg/apis/authorization"
)

type fakeAuthorizer struct {
	attrs authorizer.Attributes

	decision authorizer.Decision
	reason   string
	err      error
}

func (f *fakeAuthorizer) Authorize(ctx context.Context, attrs authorizer.Attributes) (authorizer.Decision, string, error) {
	f.attrs = attrs
	return f.decision, f.reason, f.err
}

func TestCreate(t *testing.T) {
	testcases := map[string]struct {
		spec     authorizationapi.SubjectAccessReviewSpec
		decision authorizer.Decision
		reason   string
		err      error

		expectedErr    string
		expectedAttrs  authorizer.Attributes
		expectedStatus authorizationapi.SubjectAccessReviewStatus
	}{
		"empty": {
			expectedErr: "nonResourceAttributes or resourceAttributes",
		},

		"nonresource rejected": {
			spec: authorizationapi.SubjectAccessReviewSpec{
				User:                  "bob",
				NonResourceAttributes: &authorizationapi.NonResourceAttributes{Verb: "get", Path: "/mypath"},
			},
			decision: authorizer.DecisionNoOpinion,
			reason:   "myreason",
			err:      errors.New("myerror"),
			expectedAttrs: authorizer.AttributesRecord{
				User:            &user.DefaultInfo{Name: "bob"},
				Verb:            "get",
				Path:            "/mypath",
				ResourceRequest: false,
			},
			expectedStatus: authorizationapi.SubjectAccessReviewStatus{
				Allowed:         false,
				Reason:          "myreason",
				EvaluationError: "myerror",
			},
		},

		"nonresource allowed": {
			spec: authorizationapi.SubjectAccessReviewSpec{
				User:                  "bob",
				NonResourceAttributes: &authorizationapi.NonResourceAttributes{Verb: "get", Path: "/mypath"},
			},
			decision: authorizer.DecisionAllow,
			reason:   "allowed",
			err:      nil,
			expectedAttrs: authorizer.AttributesRecord{
				User:            &user.DefaultInfo{Name: "bob"},
				Verb:            "get",
				Path:            "/mypath",
				ResourceRequest: false,
			},
			expectedStatus: authorizationapi.SubjectAccessReviewStatus{
				Allowed:         true,
				Reason:          "allowed",
				EvaluationError: "",
			},
		},

		"resource rejected": {
			spec: authorizationapi.SubjectAccessReviewSpec{
				User: "bob",
				ResourceAttributes: &authorizationapi.ResourceAttributes{
					Namespace:   "myns",
					Verb:        "create",
					Group:       "extensions",
					Version:     "v1beta1",
					Resource:    "deployments",
					Subresource: "scale",
					Name:        "mydeployment",
				},
			},
			decision: authorizer.DecisionNoOpinion,
			reason:   "myreason",
			err:      errors.New("myerror"),
			expectedAttrs: authorizer.AttributesRecord{
				User:            &user.DefaultInfo{Name: "bob"},
				Namespace:       "myns",
				Verb:            "create",
				APIGroup:        "extensions",
				APIVersion:      "v1beta1",
				Resource:        "deployments",
				Subresource:     "scale",
				Name:            "mydeployment",
				ResourceRequest: true,
			},
			expectedStatus: authorizationapi.SubjectAccessReviewStatus{
				Allowed:         false,
				Denied:          false,
				Reason:          "myreason",
				EvaluationError: "myerror",
			},
		},

		"resource allowed": {
			spec: authorizationapi.SubjectAccessReviewSpec{
				User: "bob",
				ResourceAttributes: &authorizationapi.ResourceAttributes{
					Namespace:   "myns",
					Verb:        "create",
					Group:       "extensions",
					Version:     "v1beta1",
					Resource:    "deployments",
					Subresource: "scale",
					Name:        "mydeployment",
				},
			},
			decision: authorizer.DecisionAllow,
			reason:   "allowed",
			err:      nil,
			expectedAttrs: authorizer.AttributesRecord{
				User:            &user.DefaultInfo{Name: "bob"},
				Namespace:       "myns",
				Verb:            "create",
				APIGroup:        "extensions",
				APIVersion:      "v1beta1",
				Resource:        "deployments",
				Subresource:     "scale",
				Name:            "mydeployment",
				ResourceRequest: true,
			},
			expectedStatus: authorizationapi.SubjectAccessReviewStatus{
				Allowed:         true,
				Denied:          false,
				Reason:          "allowed",
				EvaluationError: "",
			},
		},

		"resource denied": {
			spec: authorizationapi.SubjectAccessReviewSpec{
				User:               "bob",
				ResourceAttributes: &authorizationapi.ResourceAttributes{},
			},
			decision: authorizer.DecisionDeny,
			expectedAttrs: authorizer.AttributesRecord{
				User:            &user.DefaultInfo{Name: "bob"},
				ResourceRequest: true,
				APIVersion:      "*",
			},
			expectedStatus: authorizationapi.SubjectAccessReviewStatus{
				Allowed: false,
				Denied:  true,
			},
		},

		"resource denied, valid selectors": {
			spec: authorizationapi.SubjectAccessReviewSpec{
				User: "bob",
				ResourceAttributes: &authorizationapi.ResourceAttributes{
					FieldSelector: &authorizationapi.FieldSelectorAttributes{RawSelector: "foo=bar"},
					LabelSelector: &authorizationapi.LabelSelectorAttributes{RawSelector: "key=value"},
				},
			},
			decision: authorizer.DecisionDeny,
			expectedAttrs: authorizer.AttributesRecord{
				User:                      &user.DefaultInfo{Name: "bob"},
				ResourceRequest:           true,
				APIVersion:                "*",
				FieldSelectorRequirements: fields.Requirements{{Operator: "=", Field: "foo", Value: "bar"}},
				LabelSelectorRequirements: mustParse("key=value"),
			},
			expectedStatus: authorizationapi.SubjectAccessReviewStatus{
				Allowed: false,
				Denied:  true,
			},
		},
		"resource denied, invalid selectors": {
			spec: authorizationapi.SubjectAccessReviewSpec{
				User: "bob",
				ResourceAttributes: &authorizationapi.ResourceAttributes{
					FieldSelector: &authorizationapi.FieldSelectorAttributes{RawSelector: "key in value"},
					LabelSelector: &authorizationapi.LabelSelectorAttributes{RawSelector: "&"},
				},
			},
			decision: authorizer.DecisionDeny,
			expectedAttrs: authorizer.AttributesRecord{
				User:            &user.DefaultInfo{Name: "bob"},
				ResourceRequest: true,
				APIVersion:      "*",
			},
			expectedStatus: authorizationapi.SubjectAccessReviewStatus{
				Allowed:         false,
				Denied:          true,
				EvaluationError: `spec.resourceAttributes.fieldSelector ignored due to parse error; spec.resourceAttributes.labelSelector ignored due to parse error`,
			},
		},
	}

	for k, tc := range testcases {
		auth := &fakeAuthorizer{
			decision: tc.decision,
			reason:   tc.reason,
			err:      tc.err,
		}
		storage := NewREST(auth)

		result, err := storage.Create(genericapirequest.NewContext(), &authorizationapi.SubjectAccessReview{Spec: tc.spec}, rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
		if err != nil {
			if tc.expectedErr != "" {
				if !strings.Contains(err.Error(), tc.expectedErr) {
					t.Errorf("%s: expected %s to contain %q", k, err, tc.expectedErr)
				}
			} else {
				t.Errorf("%s: %v", k, err)
			}
			continue
		}
		gotAttrs := auth.attrs.(authorizer.AttributesRecord)
		if tc.expectedStatus.EvaluationError != "" {
			gotAttrs.FieldSelectorParsingErr = nil
			gotAttrs.LabelSelectorParsingErr = nil
		}
		if !reflect.DeepEqual(gotAttrs, tc.expectedAttrs) {
			t.Errorf("%s: expected\n%#v\ngot\n%#v", k, tc.expectedAttrs, gotAttrs)
		}
		status := result.(*authorizationapi.SubjectAccessReview).Status
		if !reflect.DeepEqual(status, tc.expectedStatus) {
			t.Errorf("%s: expected\n%#v\ngot\n%#v", k, tc.expectedStatus, status)
		}
	}
}

func mustParse(s string) labels.Requirements {
	selector, err := labels.Parse(s)
	if err != nil {
		panic(err)
	}
	reqs, _ := selector.Requirements()
	return reqs
}
