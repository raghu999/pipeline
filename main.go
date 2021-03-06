package main

import (
	"fmt"
	"os"

	"github.com/Depado/ginprom"
	"github.com/banzaicloud/pipeline/api"
	"github.com/banzaicloud/pipeline/audit"
	"github.com/banzaicloud/pipeline/auth"
	"github.com/banzaicloud/pipeline/config"
	"github.com/banzaicloud/pipeline/dns/route53/model"
	"github.com/banzaicloud/pipeline/model"
	"github.com/banzaicloud/pipeline/model/defaults"
	"github.com/banzaicloud/pipeline/notify"
	"github.com/banzaicloud/pipeline/objectstore"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"net/http"
)

//Version of Pipeline
var Version string

//GitRev of Pipeline
var GitRev string

//Common logger for package
var log *logrus.Logger
var logger *logrus.Entry

func initLog() *logrus.Entry {
	log = config.Logger()
	logger := log.WithFields(logrus.Fields{"state": "init"})
	return logger
}

func main() {

	if len(os.Args) > 1 && os.Args[1] == "--version" {
		if GitRev == "" {
			fmt.Println("version:", Version)
		} else {
			fmt.Printf("version: %s-%s\n", Version, GitRev)
		}
		os.Exit(0)
	}

	logger = initLog()
	logger.Info("Pipeline initialization")

	// Ensure DB connection
	db := model.GetDB()
	// Initialize auth
	auth.Init()

	// Creating tables if not exists
	logger.Info("Create table(s):",
		model.ClusterModel.TableName(model.ClusterModel{}),
		model.AmazonClusterModel.TableName(model.AmazonClusterModel{}),
		model.AzureClusterModel.TableName(model.AzureClusterModel{}),
		model.AzureNodePoolModel.TableName(model.AzureNodePoolModel{}),
		model.GoogleClusterModel.TableName(model.GoogleClusterModel{}),
		model.GoogleNodePoolModel.TableName(model.GoogleNodePoolModel{}),
	)

	// Create tables
	if err := db.AutoMigrate(
		&model.ClusterModel{},
		&model.AmazonClusterModel{},
		&model.AmazonNodePoolsModel{},
		&model.AzureClusterModel{},
		&model.AzureNodePoolModel{},
		&model.GoogleClusterModel{},
		&model.GoogleNodePoolModel{},
		&model.DummyClusterModel{},
		&model.KubernetesClusterModel{},
		&model.Deployment{},
		&model.Application{},
		&auth.AuthIdentity{},
		&auth.User{},
		&auth.UserOrganization{},
		&auth.Organization{},
		&audit.AuditEvent{},
		&defaults.AWSProfile{},
		&defaults.AWSNodePoolProfile{},
		&defaults.AKSProfile{},
		&defaults.AKSNodePoolProfile{},
		&defaults.GKEProfile{},
		&defaults.GKENodePoolProfile{},
		&objectstore.ManagedAmazonBucket{},
		&objectstore.ManagedAzureBlobStore{},
		&objectstore.ManagedGoogleBucket{},
		&route53model.Route53Domain{},
	).Error; err != nil {

		panic(err)
	}

	defaults.SetDefaultValues()

	//Initialise Gin router
	router := gin.New()

	// These two paths can contain sensitive information, so it is advised not to log them out.
	skipPaths := viper.GetStringSlice("audit.skippaths")
	router.Use(gin.LoggerWithWriter(gin.DefaultWriter, skipPaths...))
	router.Use(gin.Recovery())
	router.Use(cors.New(config.GetCORS()))
	if viper.GetBool("audit.enabled") {
		log.Infoln("Audit enabled, installing Gin audit middleware")
		router.Use(audit.LogWriter(skipPaths, viper.GetStringSlice("audit.headers")))
	}

	root := router.Group("/")
	{
		root.GET("/", api.RedirectRoot)
	}
	// Add prometheus metric endpoint
	if viper.GetBool("metrics.enabled") {
		p := ginprom.New(
			ginprom.Subsystem("gin"),
		)
		p.Use(router)
		router.Use(p.Instrument())
		http.Handle(viper.GetString("metrics.path"), promhttp.Handler())
		go http.ListenAndServe("metrics.port", nil)
	}

	auth.Install(router)

	basePath := viper.GetString("pipeline.basepath")
	v1 := router.Group(basePath + "/api/v1/")
	v1.GET("/functions", api.ListFunctions)
	{
		v1.Use(auth.Handler)
		v1.Use(auth.NewAuthorizer())
		orgs := v1.Group("/orgs")
		{
			orgs.Use(api.OrganizationMiddleware)

			orgs.GET("/:orgid/applications", api.GetApplications)
			orgs.POST("/:orgid/applications", api.CreateApplication)
			orgs.GET("/:orgid/applications/:id", api.ApplicationDetails)
			orgs.DELETE("/:orgid/applications/:id", api.DeleteApplications)

			orgs.GET("/:orgid/catalogs", api.GetCatalogs)
			orgs.GET("/:orgid/catalogs/:name", api.CatalogDetails)

			orgs.POST("/:orgid/clusters", api.CreateClusterRequest)
			//v1.GET("/status", api.Status)
			orgs.GET("/:orgid/clusters", api.FetchClusters)
			orgs.GET("/:orgid/clusters/:id", api.GetClusterStatus)
			orgs.GET("/:orgid/clusters/:id/details", api.FetchCluster)
			orgs.PUT("/:orgid/clusters/:id", api.UpdateCluster)
			orgs.PUT("/:orgid/clusters/:id/posthooks", api.ReRunPostHooks)
			orgs.POST("/:orgid/clusters/:id/secrets", api.InstallSecretsToCluster)
			orgs.DELETE("/:orgid/clusters/:id", api.DeleteCluster)
			orgs.HEAD("/:orgid/clusters/:id", api.FetchCluster)
			orgs.GET("/:orgid/clusters/:id/config", api.GetClusterConfig)
			orgs.GET("/:orgid/clusters/:id/apiendpoint", api.GetApiEndpoint)
			orgs.GET("/:orgid/clusters/:id/nodes", api.GetClusterNodes)
			orgs.POST("/:orgid/clusters/:id/monitoring", api.UpdateMonitoring)
			orgs.GET("/:orgid/clusters/:id/endpoints", api.ListEndpoints)
			orgs.GET("/:orgid/clusters/:id/deployments", api.ListDeployments)
			orgs.POST("/:orgid/clusters/:id/deployments", api.CreateDeployment)
			orgs.HEAD("/:orgid/clusters/:id/deployments", api.GetTillerStatus)
			orgs.DELETE("/:orgid/clusters/:id/deployments/:name", api.DeleteDeployment)
			orgs.PUT("/:orgid/clusters/:id/deployments/:name", api.UpgradeDeployment)
			orgs.HEAD("/:orgid/clusters/:id/deployments/:name", api.HelmDeploymentStatus)
			orgs.POST("/:orgid/clusters/:id/helminit", api.InitHelmOnCluster)
			orgs.GET("/:orgid/helm/repos", api.HelmReposGet)
			orgs.POST("/:orgid/helm/repos", api.HelmReposAdd)
			orgs.PUT("/:orgid/helm/repos/:name", api.HelmReposModify)
			orgs.PUT("/:orgid/helm/repos/:name/update", api.HelmReposUpdate)
			orgs.DELETE("/:orgid/helm/repos/:name", api.HelmReposDelete)
			orgs.GET("/:orgid/helm/charts", api.HelmCharts)
			orgs.GET("/:orgid/helm/chart/:reponame/:name/:version", api.HelmChart)
			orgs.GET("/:orgid/helm/chart/:reponame/:name", api.HelmChart)
			orgs.GET("/:orgid/profiles/cluster/:type", api.GetClusterProfiles)
			orgs.POST("/:orgid/profiles/cluster", api.AddClusterProfile)
			orgs.PUT("/:orgid/profiles/cluster", api.UpdateClusterProfile)
			orgs.DELETE("/:orgid/profiles/cluster/:type/:name", api.DeleteClusterProfile)
			orgs.GET("/:orgid/secrets", api.ListSecrets)
			orgs.POST("/:orgid/secrets", api.AddSecrets)
			orgs.PUT("/:orgid/secrets/:secretid", api.UpdateSecrets)
			orgs.DELETE("/:orgid/secrets/:secretid", api.DeleteSecrets)
			orgs.GET("/:orgid/users", api.GetUsers)
			orgs.GET("/:orgid/users/:id", api.GetUsers)
			orgs.POST("/:orgid/users/:id", api.AddUser)
			orgs.DELETE("/:orgid/users/:id", api.RemoveUser)

			orgs.GET("/:orgid/buckets", api.ListObjectStoreBuckets)
			orgs.POST("/:orgid/buckets", api.CreateObjectStoreBuckets)
			orgs.HEAD("/:orgid/buckets/:name", api.CheckObjectStoreBucket)
			orgs.DELETE("/:orgid/buckets/:name", api.DeleteObjectStoreBucket)

			orgs.GET("/:orgid/cloudinfo", api.GetSupportedClusterList)
			orgs.GET("/:orgid/cloudinfo/:cloudtype", api.GetCloudInfo)

			orgs.GET("/:orgid/allowed/secrets/", api.ListAllowedSecretTypes)
			orgs.GET("/:orgid/allowed/secrets/:type", api.ListAllowedSecretTypes)

			orgs.GET("/:orgid", api.GetOrganizations)
			orgs.DELETE("/:orgid", api.DeleteOrganization)
		}
		v1.GET("/orgs", api.GetOrganizations)
		v1.POST("/orgs", api.CreateOrganization)
		v1.GET("/token", auth.GenerateToken) // TODO Deprecated, should be removed once the UI has support.
		v1.POST("/tokens", auth.GenerateToken)
		v1.GET("/tokens", auth.GetTokens)
		v1.GET("/tokens/:id", auth.GetTokens)
		v1.DELETE("/tokens/:id", auth.DeleteToken)
	}

	router.GET(basePath+"/api", api.MetaHandler(router, basePath+"/api"))

	notify.SlackNotify("API is already running")
	var listenPort string
	port := viper.GetInt("pipeline.listenport")
	if port != 0 {
		listenPort = fmt.Sprintf(":%d", port)
		logger.Info("Pipeline API listening on port ", listenPort)
	}

	certFile, keyFile := viper.GetString("pipeline.certfile"), viper.GetString("pipeline.keyfile")
	if certFile != "" && keyFile != "" {
		router.RunTLS(listenPort, certFile, keyFile)
	} else {
		router.Run(listenPort)
	}
}
