angular.module('openshiftConsole')
  .directive('podTemplate', function() {
    return {
      restrict: 'E',    
      templateUrl: 'views/_pod-template.html'
    };
  })
  .directive('pods', function() {
    return {
      restrict: 'E',
      scope: {
        pods: '='
      },
      templateUrl: 'views/_pods.html'
    };
  })
  .directive('triggers', function() {
    return {
      restrict: 'E',
      scope: {
        triggers: '='
      },      
      templateUrl: 'views/_triggers.html'
    };
  })
  .directive('deploymentConfigMetadata', function() {
    return {
      restrict: 'E',
      scope: {
        deploymentConfigId: '=',
        exists: '=',
        differentService: '='
      },
      templateUrl: 'views/_deployment-config-metadata.html'
    };
  })
  .directive('templates', function() {
    return {
      restrict: 'E',
      scope: {
        list: '=list',
        title: '=title'
      },
      templateUrl: 'views/_templates.html'
    }
  })
  .directive("template", function() {
    return {
      restrict: 'E',
      templateUrl: 'views/_template.html'
    }
  })
  .directive("templateResult", function() {
    return {
      restrict: 'E',
      templateUrl: 'views/_template_result.html'
    }
  });
