'use strict';

/**
 * @ngdoc function
 * @name openshiftConsole.controller:NewFromTemplateController
 * @description
 * # NewFromTemplateController
 * Controller of the openshiftConsole
 */
angular.module('openshiftConsole')
  .controller('NewFromTemplateController', function ($scope, $http, $routeParams, DataService, $q, $location, TaskList) {

    function errorPage(message) {
      var redirect = URI('/error').query({
      	error_description: message,
      }).toString();
      $location.url(redirect);
    }

    function imageItems(data) {
      var images = []
      data.items.forEach(function(item) {
        if (item.kind == "ImageRepository") {
          images.push(item)
        }
      })
      return images
    }
    
    $scope.createFromTemplate = function() { 
      DataService.create("templateConfigs", $scope.template, $scope).then(
        function(config) { // success
          var titles = { 
            started: "Creating " + $scope.template.metadata.name + " in project " + $scope.projectName,
            success: "Created " + $scope.template.metadata.name + " in project " + $scope.projectName,
            failure: "Failed to create " + $scope.template.metadata.name + " in project " + $scope.projectName
          };
          // TODO: Determine how to generate Help Links - would they be annotations on the template?
          var helpLinks = [ { title:"Additional Information", link: "https://github.com/openshift/origin" } ];
          TaskList.add(titles, helpLinks, function() {
            var d = $q.defer()
            DataService.createList(config.items, $scope).then(
              function(result) {
                var alerts = [];
                var hasErrors = false;
                if (result.failure.length > 0) {
                  result.failure.forEach(
                    function(failure) {
                      var objectName = "";
                      if (failure.data && failure.data.details) {
                        objectName = failure.data.details.kind + " " + failure.data.details.id;
                      }
                      alerts.push({ type: "error", message: "Cannot create " + objectName, details: failure.data.message })
                      hasErrors = true;
                    }
                  );
                } else {
                  alerts.push({ type: "success", message: "All items in template " + $scope.template.metadata.name +
                    " were created successfully."});
                }
                d.resolve({alerts: alerts, hasErrors: hasErrors});
              }
            );
            return d.promise;
          });
          $location.path("/project/" + $scope.projectName + "/overview");
        },
        function(result) { // failure
          $scope.alerts = [
            { 
              type: "error", 
              message: "An error occurred processing the template.",
              details: "Status: " + result.status + ". " + result.data,
            }
          ]
        }
      );
    }
    
    $scope.toggleOptionsExpanded = function() {
      $scope.optionsExpanded = !$scope.optionsExpanded
    }
    
    $scope.paramPlaceholder = function(param) {
      if (param.generate) {
        return "(generated)"
      } else {
        return ""
      }
    }
    
    $scope.paramValue = function(param) {
      if (!param.value && param.generate) {
        return "(generated)"
      } else {
        return param.value
      }
    }
    
    
    var name = $routeParams.name;
    var namespace = $routeParams.namespace;
    var url = $routeParams.url;
    
    if (!name && !url) {
      errorPage("Cannot create template: a template name or URL was not specified.");
      return;
    }

    $scope.templateUrl = url;
    $scope.emptyMessage = "Loading..."; 
    $scope.alerts = [];
    $scope.projectPromise = $.Deferred();
    $scope.projectName = $routeParams.project
    $scope.projectPromise.resolve({ metadata: { name: $scope.projectName }});

    DataService.getTemplate(name, namespace, url, $scope).then(
      function(template) {
        $scope.template = template;
        $scope.templateImages = imageItems(template);
        $scope.hasParameters = $scope.template.parameters && $scope.template.parameters.length > 0;
        $scope.optionsExpanded = true;
        template.labels = template.labels || {};
      },
      function(data) {
        errorPage("Cannot create template: the specified template could not be retrieved.");
      }
    );
  });
