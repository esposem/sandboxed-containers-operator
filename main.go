/*


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

package main

import (
	"context"
	"flag"
	"os"

	secv1 "github.com/openshift/api/security/v1"
	mcfgapi "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io"
	"go.uber.org/zap/zapcore"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	nodeapi "k8s.io/kubernetes/pkg/apis/node/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	kataconfigurationv1 "github.com/openshift/sandboxed-containers-operator/api/v1"
	"github.com/openshift/sandboxed-containers-operator/controllers"
	// +kubebuilder:scaffold:imports
)

const (
	OperatorNamespace = "openshift-sandboxed-containers-operator"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(nodeapi.AddToScheme(scheme))

	utilruntime.Must(secv1.AddToScheme(scheme))

	utilruntime.Must(mcfgapi.Install(scheme))

	utilruntime.Must(kataconfigurationv1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func SetTimeEncoderToRfc3339() zap.Opts {
	return func(o *zap.Options) {
		o.TimeEncoder = zapcore.RFC3339TimeEncoder
	}
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true), SetTimeEncoderToRfc3339()))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: metricsAddr,
		Port:               9443,
		LeaderElection:     enableLeaderElection,
		LeaderElectionID:   "290f4947.kataconfiguration.openshift.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	isOpenshift, err := controllers.IsOpenShift()
	if err != nil {
		setupLog.Error(err, "unable to use discovery client")
		os.Exit(1)
	}

	if isOpenshift {

		err = fixScc(context.TODO(), mgr)
		if err != nil {
			setupLog.Error(err, "unable to create SCC")
			os.Exit(1)
		}

		err = labelNamespace(context.TODO(), mgr)
		if err != nil {
			setupLog.Error(err, "unable to add labels to namespace")
			os.Exit(1)
		}

		setupLog.Info("added labels")

		// Create the OS detection struct
		var operatingSystem, err = controllers.NewOperatingSystem(controllers.EtcOSReleasePath, controllers.LibOSReleasePath)
		if err != nil {
			setupLog.Error(err, "unable to detect operating system")
			os.Exit(1)
		}

		if err = (&controllers.KataConfigOpenShiftReconciler{
			Client: mgr.GetClient(),
			Log:    ctrl.Log.WithName("controllers").WithName("KataConfig"),
			Scheme: mgr.GetScheme(),
			Os:     operatingSystem,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create KataConfig controller for OpenShift cluster", "controller", "KataConfig")
			os.Exit(1)
		}
	}

	if err = (&kataconfigurationv1.KataConfig{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "KataConfig")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func fixScc(ctx context.Context, mgr manager.Manager) error {

	scc := controllers.GetScc()
	err := mgr.GetAPIReader().Get(ctx, client.ObjectKeyFromObject(scc), scc)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Nothing to do.
			err = nil
		}
	} else if scc.SELinuxContext.Type == secv1.SELinuxStrategyMustRunAs {
		// A 1.2-style SCC breaks the MCO. This was fixed by
		// commit d4745883e38f, i.e. OSC >= 1.3 doesn't create
		// broken SCC anymore, but an existing instance still
		// needs to be fixed.
		setupLog.Info("Fixing SCC")
		scc.SELinuxContext = secv1.SELinuxContextStrategyOptions{
			Type: secv1.SELinuxStrategyRunAsAny,
		}
		err = mgr.GetClient().Update(ctx, scc)
	}

	return err
}

func labelNamespace(ctx context.Context, mgr manager.Manager) error {

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: OperatorNamespace,
		},
	}
	err := mgr.GetAPIReader().Get(ctx, client.ObjectKeyFromObject(ns), ns)
	if err != nil {
		setupLog.Error(err, "Unable to add label to the namespace")
		return err
	}

	setupLog.Info("Labelling Namespace")
	setupLog.Info("Labels: ", "Labels", ns.ObjectMeta.Labels)
	// Add namespace label to align with newly introduced Pod Security Admission controller
	ns.ObjectMeta.Labels["openshift.io/cluster-monitoring"] = "true"
	ns.ObjectMeta.Labels["pod-security.kubernetes.io/enforce"] = "privileged"
	ns.ObjectMeta.Labels["pod-security.kubernetes.io/audit"] = "privileged"
	ns.ObjectMeta.Labels["pod-security.kubernetes.io/warn"] = "privileged"

	return mgr.GetClient().Update(ctx, ns)
}
