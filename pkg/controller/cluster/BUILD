load("@io_bazel_rules_go//go:def.bzl", "go_library", "go_test")

go_library(
    name = "go_default_library",
    srcs = [
        "controller.go",
        "zz_generated.api.register.go",
    ],
    importpath = "sigs.k8s.io/cluster-api/pkg/controller/cluster",
    visibility = ["//visibility:public"],
    deps = [
        "//pkg/apis/cluster/v1alpha1:go_default_library",
        "//pkg/client/listers_generated/cluster/v1alpha1:go_default_library",
        "//pkg/controller/sharedinformers:go_default_library",
        "//vendor/github.com/golang/glog:go_default_library",
        "//vendor/github.com/kubernetes-incubator/apiserver-builder/pkg/builders:go_default_library",
        "//vendor/github.com/kubernetes-incubator/apiserver-builder/pkg/controller:go_default_library",
        "//vendor/k8s.io/apimachinery/pkg/api/errors:go_default_library",
        "//vendor/k8s.io/client-go/rest:go_default_library",
        "//vendor/k8s.io/client-go/tools/cache:go_default_library",
        "//vendor/k8s.io/client-go/util/workqueue:go_default_library",
    ],
)

go_test(
    name = "go_default_xtest",
    srcs = [
        "cluster_suite_test.go",
        "controller_test.go",
    ],
    importpath = "sigs.k8s.io/cluster-api/pkg/controller/cluster_test",
    deps = [
        ":go_default_library",
        "//pkg/apis:go_default_library",
        "//pkg/apis/cluster/v1alpha1:go_default_library",
        "//pkg/client/clientset_generated/clientset:go_default_library",
        "//pkg/client/clientset_generated/clientset/typed/cluster/v1alpha1:go_default_library",
        "//pkg/controller/sharedinformers:go_default_library",
        "//pkg/openapi:go_default_library",
        "//vendor/github.com/kubernetes-incubator/apiserver-builder/pkg/test:go_default_library",
        "//vendor/k8s.io/apimachinery/pkg/apis/meta/v1:go_default_library",
        "//vendor/k8s.io/client-go/rest:go_default_library",
    ],
)
