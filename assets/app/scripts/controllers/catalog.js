'use strict';

/**
 * @ngdoc function
 * @name openshiftConsole.controller:PodsController
 * @description
 * # ProjectController
 * Controller of the openshiftConsole
 */
angular.module('openshiftConsole')
  .controller('CatalogController', function ($scope, DataService, $filter, LabelFilter) {
    $scope.searchText = "";
    
    //TODO: These will eventually come from the server
    $scope.myTemplates = [
      {
        name: "MyApp Dev",
        description: "Brief description of the template",
        category: "Template"
        icon: "https://www.openshift.com/app/assets/logo-online-horizontal.svg"
      },
      {
        name: "MyApp Test",
        description: "Brief description of the template",
        category: 
      }
    ];
    $scope.myOrgTemplates = [
      {
        
      }
    ];
    $scope.myOrgTitle = "MyOrg";
    $scope.featured = [
      {
        title: "Runtimes",
        templates: [
        
        ]
      },
      {
        title: "Databases",
        templates: [
        
        ]
      },
      {
        title: "Messaging",
        templates: [
        
        ]
      }
    ];
    $scope.languages = [
      "Go",
      "Java",
      "JavaScript",
      "PHP",
      "Python",
      "Ruby",
      "Scala"
    ];
    $scope.categories = [
      "Templates",
      "Runtimes",
      "Databases",
      "Database Monitoring",
      "Messaging",
      "Caching",
      "Performance"
    ];
    
  });