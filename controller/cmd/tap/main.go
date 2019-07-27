package main

import (
	"flag"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/linkerd/linkerd2/controller/k8s"
	"github.com/linkerd/linkerd2/controller/tap"
	"github.com/linkerd/linkerd2/pkg/admin"
	"github.com/linkerd/linkerd2/pkg/flags"
	pkgk8s "github.com/linkerd/linkerd2/pkg/k8s"
	log "github.com/sirupsen/logrus"
)

type apiServiceHandler struct {
	http.Handler
}

func (h *apiServiceHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// TODO: https://kubernetes.io/docs/tasks/access-kubernetes-api/configure-aggregation-layer/#extension-apiserver-authenticates-the-request
	log.Infof("ServeHTTP() req: %+v", req)
}

func main() {
	addr := flag.String("addr", ":8088", "address to serve on")
	apiServiceAddr := flag.String("apiservice-addr", ":8089", "address to serve the APIService on")
	metricsAddr := flag.String("metrics-addr", ":9998", "address to serve scrapable metrics on")
	kubeConfigPath := flag.String("kubeconfig", "", "path to kube config")
	controllerNamespace := flag.String("controller-namespace", "linkerd", "namespace in which Linkerd is installed")
	tapPort := flag.Uint("tap-port", 4190, "proxy tap port to connect to")
	flags.ConfigureAndParse()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	log.Infof("TESTING starting APIService server on %s", *apiServiceAddr)

	k8sAPI, err := k8s.InitializeAPI(
		*kubeConfigPath,
		k8s.DS,
		k8s.SS,
		k8s.Deploy,
		k8s.Job,
		k8s.NS,
		k8s.Pod,
		k8s.RC,
		k8s.Svc,
		k8s.RS,
	)
	if err != nil {
		log.Fatalf("Failed to initialize K8s API: %s", err)
	}

	server, lis, err := tap.NewServer(*addr, *tapPort, *controllerNamespace, k8sAPI)
	if err != nil {
		log.Fatal(err.Error())
	}

	k8sAPI.Sync() // blocks until caches are synced

	go func() {
		log.Infof("starting gRPC server on %s", *addr)
		server.Serve(lis)
	}()

	go func() {
		// cred, err := tls.ReadPEMCreds(pkgk8s.MountPathTLSKeyPEM, pkgk8s.MountPathTLSCrtPEM)
		// if err != nil {
		// 	log.Fatalf("failed to read TLS secrets: %s", err)
		// }

		// var (
		// 	certPEM = cred.EncodePEM()
		// 	keyPEM  = cred.EncodePrivateKeyPEM()
		// )

		// cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
		// if err != nil {
		// 	return nil, err
		// }

		// server := &http.Server{
		// 	Addr: addr,
		// 	TLSConfig: &tls.Config{
		// 		Certificates: []tls.Certificate{cert},
		// 	},
		// }

		// s := &Server{server, api, handler}
		// s.Handler = http.HandlerFunc(s.serve)
		// return s, nil

		log.Infof("starting APIService server on %s", *apiServiceAddr)

		lis, err := net.Listen("tcp", *apiServiceAddr)
		if err != nil {
			log.Fatal(err.Error())
		}
		apiService := &http.Server{
			Addr:    *apiServiceAddr,
			Handler: &apiServiceHandler{},
			// TLSConfig: tlsCfg,
		}
		err = apiService.ServeTLS(lis, pkgk8s.MountPathTLSCrtPEM, pkgk8s.MountPathTLSKeyPEM)
		if err != nil {
			log.Fatal(err.Error())
		}
	}()

	go admin.StartServer(*metricsAddr)

	<-stop

	log.Infof("shutting down gRPC server on %s", *addr)
	server.GracefulStop()
}
