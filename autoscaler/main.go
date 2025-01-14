package main

import (
  "os"
  "fmt"
  "time"
  "strconv"
  "errors"
  "context"
  "k8s.io/client-go/kubernetes"
  "k8s.io/client-go/util/retry"
  "k8s.io/apimachinery/pkg/labels"
  rabbithole "github.com/michaelklishin/rabbit-hole/v2"
  kubeinformers "k8s.io/client-go/informers"
  corev1 "k8s.io/api/core/v1"
  appsv1 "k8s.io/api/apps/v1"
  metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
  log "github.com/sirupsen/logrus"
  applisters "k8s.io/client-go/listers/apps/v1"
  configparser "github.com/bigkevmcd/go-configparser"
  rabbitplugin "github.com/alexnjh/epsilon/autoscaler/plugins/rabbitmq"
  queueplugin "github.com/alexnjh/epsilon/autoscaler/plugins/queue_theory"
  linearplugin "github.com/alexnjh/epsilon/autoscaler/plugins/linear_regression"
  schedplugin "github.com/alexnjh/epsilon/autoscaler/plugins/scheduler_prob"
  "github.com/alexnjh/epsilon/autoscaler/interfaces"
)

const (
  // Default microservice configuration file
  DefaultConfigPath = "/go/src/app/config.cfg"
)

/*

The main routing of the autoscaler.

The autoscaler will first attempt to get configuration variables via the config file.
If not config file is found the autoscaler will attempt to load configuration variables
from the Environment variables.

Once the configuration variables are loaded the autoscaler will start intitalizing the
different plugins. Once the plugins are initilize the autoscaler will gather cluster
metrics and call the different plugins and wait for the plugins to return a decision.

Once a decision is made the autoscaler weill proceed to execute the decision. This process
will continue after a certain interval that is specified by the user.
*/
func main() {

  // Get required values
  confDir := os.Getenv("CONFIG_DIR")

  var config *configparser.ConfigParser
  var err error

  if len(confDir) != 0 {
    config, err = getConfig(confDir)
  }else{
    config, err = getConfig(DefaultConfigPath)
  }

  var mqHost, mqManagePort, mqUser, mqPass, namespace, defaultQueue, pcURL, updateInterval string

  if err != nil {

    log.Errorf(err.Error())

    namespace = os.Getenv("POD_NAMESPACE")
    mqHost = os.Getenv("MQ_HOST")
    mqManagePort = os.Getenv("MQ_MANAGE_PORT")
    mqUser = os.Getenv("MQ_USER")
    mqPass = os.Getenv("MQ_PASS")
    defaultQueue = os.Getenv("DEFAULT_QUEUE")
    updateInterval = os.Getenv("INTERVAL")
    pcURL = os.Getenv("PC_METRIC_URL")

    if len(mqHost) == 0 ||
    len(mqManagePort) == 0 ||
    len(mqUser) == 0 ||
    len(mqPass) == 0 ||
    len(defaultQueue) == 0 ||
    len(namespace) == 0 ||
    len(pcURL) == 0 ||
    len(updateInterval) == 0{
  	   log.Fatalf("Config not found, Environment variables missing")
    }


  }else{

    mqHost, err = config.Get("QueueService", "hostname")
    if err != nil {
      log.Fatalf(err.Error())
    }
    mqManagePort, err = config.Get("QueueService", "management_port")
    if err != nil {
      log.Fatalf(err.Error())
    }
    mqUser, err = config.Get("QueueService", "user")
    if err != nil {
      log.Fatalf(err.Error())
    }
    mqPass, err = config.Get("QueueService", "pass")
    if err != nil {
      log.Fatalf(err.Error())
    }
    mqManagePort, err = config.Get("QueueService", "management_port")
    if err != nil {
      log.Fatalf(err.Error())
    }
    namespace, err = config.Get("DEFAULTS", "namespace")
    if err != nil {
      log.Fatalf(err.Error())
    }
    pcURL, err = config.Get("CoordinatorService", "metrics_absolute_url")
    if err != nil {
      log.Fatalf(err.Error())
    }
    updateInterval, err = config.Get("DEFAULTS", "update_interval")
    if err != nil {
      log.Fatalf(err.Error())
    }
  }

  interval, err := strconv.Atoi(updateInterval)
  if err != nil {
	   log.Fatalf(err.Error())
  }

  queueList := map[string]bool {
    fmt.Sprintf(defaultQueue): true,
  }

  // Create informers to be inform of updates to the cluster state
  kubeClient := getKubernetesClient()
  kubeInformerFactory := kubeinformers.NewSharedInformerFactory(kubeClient, time.Second*30)
  nodeInformer := kubeInformerFactory.Core().V1().Nodes().Informer()
  nodeLister := kubeInformerFactory.Core().V1().Nodes().Lister()
  deployInformer := kubeInformerFactory.Apps().V1().Deployments().Informer()
  deployLister := kubeInformerFactory.Apps().V1().Deployments().Lister()

  // use a channel to synchronize the finalization for a graceful shutdown
	stopCh := make(chan struct{})
	defer close(stopCh)

  // Start the informers
  kubeInformerFactory.Start(stopCh)

  log.Infof("Waiting for cache to be populated")
  for {
    if nodeInformer.HasSynced() && deployInformer.HasSynced(){
        log.Infof("Cache is populated\n\n")
        break
    }
  }


  // Access the rabbitmq queue microservice to get queue statistics
  rmqc, _ := rabbithole.NewClient(fmt.Sprintf("http://%s:%s",mqHost,mqManagePort), mqUser, mqPass)

  res, err := rmqc.Overview()

  if err != nil{
    log.Fatalf(err.Error())
  }

  log.Infof("RabbitMQ Server Information")
  log.Infof("---------------------------")
  log.Infof("Management version: %s",res.ManagementVersion)
  log.Infof("Erlang version: %s",res.ErlangVersion)

  pluginList := make(map[string]interfaces.AutoScalerPlugin)

  qs, err := rmqc.ListQueues()

  if err != nil{
    log.Fatalf(err.Error())
  }

  for _ , queue := range(qs){
    if queue.Name == defaultQueue{

      // Initialize Plugins
      pluginList["rabbitmq"]=rabbitplugin.NewRabbitMQPlugin("rabbitmq",queue.Vhost,queue.Name,0.5,rmqc)
      pluginList["schedprob"]=schedplugin.NewSchedProbPlugin("schedprob",queue.Name,0.5)
      pluginList["reggression"]=linearplugin.NewLinearRegressionPlugin("reggression",queue.Name,5)
      pluginList["queuetheory"]=queueplugin.NewQueueTheoryPlugin("queuetheory",0.5,fmt.Sprintf("http://%s",pcURL))

      break
    }
  }

  temp := make([]interfaces.ComputeResult,len(pluginList))


  // Main process loop
  for {

    qs, err := rmqc.ListQueues()

    if err != nil{
      log.Fatalf(err.Error())
    }

    for _ , queue := range(qs){
      if queueList[queue.Name] {

        log.Infof("Queue Name: %s\n------------------------------",queue.Name)

          nodeList, err := nodeLister.List(labels.NewSelector())

          if err != nil{
            log.Fatalf(err.Error())
          }

          noOfPendingPods := float64(queue.MessagesReady)
          noOfNodes := float64(len(nodeList))
          noOfSched := float64(queue.Consumers)

          var i = 0
          for key , plugin := range(pluginList){
            temp[i] = plugin.Compute(noOfPendingPods,noOfNodes,noOfSched)
            log.Infof("%s Decision:  %s",key,temp[i])
            i++
          }

          result := makeDecision(temp)
          UpdateDeployment(kubeClient,deployLister,namespace,queue.Name,result)
      }
    }

    log.Infof("Sleeping for %d seconds before testing again...",interval)
    time.Sleep(time.Duration(interval)*time.Second)

  }
}


// Update the replica count of the scheduler services
func UpdateDeployment(client kubernetes.Interface, lister applisters.DeploymentLister, namespace string, queueName string, decision interfaces.ComputeResult){

  if decision == interfaces.DoNotScale {
    return
  }

  labelmap := map[string]string{
    "epsilon.queue" : queueName,
  }

  var deployment []*appsv1.Deployment

  retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {

    if len(namespace) == 0{
      deployment, _ = lister.List(labels.SelectorFromSet(labelmap))
    }else{
      deployment, _ = lister.Deployments(namespace).List(labels.SelectorFromSet(labelmap))
    }

    if len(deployment) > 1 {
      return errors.New("More than one deployment found for queue name")
    }

    if len(deployment) == 0 {
      return errors.New("No deployment found for queue name")
    }

    obj := deployment[0]

    if decision == interfaces.ScaleUp {
      *obj.Spec.Replicas +=1
    }else if decision == interfaces.ScaleDown && *obj.Spec.Replicas > 1{
      *obj.Spec.Replicas -=1
    }else{
      return nil
    }

    _, updateErr := client.AppsV1().Deployments(obj.Namespace).Update(context.TODO(), obj, metav1.UpdateOptions{});
    return updateErr
  })
  if retryErr != nil {
    panic(fmt.Errorf("Update failed: %v", retryErr))
  }

}

// Consolidate all the decisions made by the plugins and decide on the next course of action.
func makeDecision(result []interfaces.ComputeResult) interfaces.ComputeResult{
  count := make(map[interfaces.ComputeResult]int)

  for _, r := range(result){
    count[r] +=1
  }


  largest := interfaces.ScaleUp

  if(count[interfaces.ScaleUp] > count[interfaces.ScaleDown]){
    largest = interfaces.ScaleUp
  }else{
    largest = interfaces.ScaleDown
  }

  if(count[interfaces.ScaleUp] == count[interfaces.ScaleDown]){
    largest = interfaces.DoNotScale
  }

  return largest

}

// Update kube-api server of the autoscaler's scale down operations
func addScaleDownEvent(client kubernetes.Interface, obj *appsv1.Deployment){
  client.CoreV1().Events(obj.Namespace).Create(context.TODO(), &corev1.Event{
    Count:          1,
    Message:        "Scheduler replica reduced by 1",
    Reason:         "ScaleDown",
    LastTimestamp:  metav1.Now(),
    FirstTimestamp: metav1.Now(),
    Type:           "Information",
    Source: corev1.EventSource{
      Component: "autoscaler",
    },
    InvolvedObject: corev1.ObjectReference{
      Kind:      "Deployment",
      Name:      obj.Name,
      Namespace: obj.Namespace,
      UID:       obj.UID,
    },
    ObjectMeta: metav1.ObjectMeta{
      GenerateName: obj.Name + "-",
    },
  },metav1.CreateOptions{})
}

// Update kube-api server of the autoscaler's scale up operations
func addScaleUpEvent(client kubernetes.Interface, obj *appsv1.Deployment){
  client.CoreV1().Events(obj.Namespace).Create(context.TODO(), &corev1.Event{
    Count:          1,
    Message:        "Scheduler replica increased by 1",
    Reason:         "ScaleUp",
    LastTimestamp:  metav1.Now(),
    FirstTimestamp: metav1.Now(),
    Type:           "Information",
    Source: corev1.EventSource{
      Component: "autoscaler",
    },
    InvolvedObject: corev1.ObjectReference{
      Kind:      "Deployment",
      Name:      obj.Name,
      Namespace: obj.Namespace,
      UID:       obj.UID,
    },
    ObjectMeta: metav1.ObjectMeta{
      GenerateName: obj.Name + "-",
    },
  },metav1.CreateOptions{})
}
